package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"path"
	"sort"
	"strings"
)

// Support for the Skills extension (SEP-2640,
// "io.modelcontextprotocol/skills", Final). A skill is an Agent Skills
// directory (minimally a SKILL.md with YAML frontmatter carrying name and
// description); every file is exposed as an ordinary MCP resource,
// conventionally under skill://<skill-path>/<file-path>, with the skill's
// name equal to the final path segment. skills/list enumerates the skills a
// server serves; skills/get returns one skill's entry (metadata and
// per-file digests) by URI; content is read with resources/read like any
// other resource. This file implements the transport binding only — the
// SKILL.md format itself belongs to the Agent Skills specification.

// SkillsExtensionID is the extension capability a server declares to
// advertise skills support, negotiated via the capabilities.extensions
// mechanism (SEP-1724).
const SkillsExtensionID = "io.modelcontextprotocol/skills"

// Skill is one skills/list or skills/get entry.
type Skill struct {
	// URI is the full resource URI of the skill's SKILL.md.
	URI string `json:"uri"`
	// Frontmatter is the SKILL.md frontmatter (at minimum name and
	// description per the Agent Skills specification).
	Frontmatter map[string]any `json:"frontmatter"`
	// Resources lists every file of the skill directory with its digest
	// (sha256 over the content bytes) and size.
	Resources []SkillResource `json:"resources"`
}

// SkillResource is one skill file's listing entry.
type SkillResource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// SkillBuilder builds a skill registration from its files.
type SkillBuilder struct {
	prefix      []string
	name        string
	frontmatter map[string]any
	files       map[string][]byte
}

// NewSkill creates a skill builder for a skill directory named name (the
// final skill-path segment; the SKILL.md frontmatter name must match it).
func NewSkill(name string) *SkillBuilder {
	return &SkillBuilder{
		name:        name,
		frontmatter: map[string]any{"name": name},
		files:       map[string][]byte{},
	}
}

// Prefix prepends organizational path segments to the skill's URI
// (e.g. Prefix("acme", "billing") yields skill://acme/billing/<name>/SKILL.md).
func (b *SkillBuilder) Prefix(segments ...string) *SkillBuilder {
	b.prefix = append(b.prefix, segments...)
	return b
}

// Description sets the frontmatter description.
func (b *SkillBuilder) Description(description string) *SkillBuilder {
	b.frontmatter["description"] = description
	return b
}

// Metadata sets a frontmatter metadata key (e.g. Metadata("version", "1.0.0")
// becomes frontmatter.metadata.version).
func (b *SkillBuilder) Metadata(key string, value any) *SkillBuilder {
	m, _ := b.frontmatter["metadata"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		b.frontmatter["metadata"] = m
	}
	m[key] = value
	return b
}

// File adds one file to the skill directory by relative path. A skill MUST
// contain SKILL.md at its root; registration panics without it (a missing
// SKILL.md is a programmer error, like an invalid UI visibility).
func (b *SkillBuilder) File(relPath string, content []byte) *SkillBuilder {
	b.files[relPath] = content
	return b
}

// RootURI is the skill's root directory URI (no trailing slash).
func (b *SkillBuilder) RootURI() string {
	return "skill://" + strings.Join(append(append([]string{}, b.prefix...), b.name), "/")
}

// EntryURI is the full URI of the skill's SKILL.md.
func (b *SkillBuilder) EntryURI() string {
	return b.RootURI() + "/SKILL.md"
}

// parseSKILLMDFrontmatter extracts a SKILL.md's YAML frontmatter block
// ("---\nkey: value\n---") into top-level scalar entries, without a YAML
// dependency. Indented (nested) lines and non key:value lines are skipped —
// the common Agent Skills frontmatter is flat scalars (name, description,
// version, license, ...), and serving those verbatim is what conformance
// checkers compare. Returns nil when the file has no frontmatter block.
func parseSKILLMDFrontmatter(content []byte) map[string]any {
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	var fm map[string]any
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '-' || line[0] == '#' {
			continue // nested content, list items, comments
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")
		if value == "" {
			continue
		}
		if fm == nil {
			fm = map[string]any{}
		}
		fm[strings.TrimSpace(key)] = value
	}
	return fm
}

// skillDigest computes the spec's digest over content bytes.
func skillDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// skillFileMIME picks a resource MIME type by extension, falling back to
// the platform guess and then application/octet-stream.
func skillFileMIME(relPath string) string {
	switch strings.ToLower(path.Ext(relPath)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".py":
		return "text/x-python"
	case ".json":
		return "application/json"
	case ".txt":
		return "text/plain"
	}
	guessed := mime.TypeByExtension(path.Ext(relPath))
	if guessed == "" {
		guessed = "application/octet-stream"
	}
	return guessed
}

// registerSkillLocked compiles the builder into a listing entry, registers
// every file as a readable resource, and stores the entry. Caller holds mu.
//
// The listing's frontmatter is parsed verbatim from the SKILL.md itself
// when it carries a frontmatter block: conformance checkers compare the
// listing against the served file, so the two must agree by construction.
// The builder's Description/Metadata calls act only as the fallback for a
// SKILL.md with no frontmatter block.
func (s *Server) registerSkillLocked(b *SkillBuilder) {
	frontmatter := parseSKILLMDFrontmatter(b.files["SKILL.md"])
	if frontmatter != nil {
		// The file's block wins entirely (only the required name is filled
		// if the file omitted it); builder-supplied fields never override
		// or augment what the served file actually declares.
		if _, ok := frontmatter["name"]; !ok {
			frontmatter["name"] = b.name
		}
	} else {
		frontmatter = b.frontmatter
	}
	entry := &Skill{
		URI:         b.EntryURI(),
		Frontmatter: frontmatter,
		Resources:   make([]SkillResource, 0, len(b.files)),
	}
	paths := make([]string, 0, len(b.files))
	for rel := range b.files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		content := b.files[rel]
		uri := b.RootURI() + "/" + rel
		entry.Resources = append(entry.Resources, SkillResource{
			URI:    uri,
			Digest: skillDigest(content),
			Size:   int64(len(content)),
		})
		fileURI, fileContent, fileRel := uri, content, rel
		s.resources[fileURI] = &registeredResource{
			descriptor: MCPResource{
				URI:         fileURI,
				Name:        fileRel,
				Description: "Skill " + b.name + " file " + fileRel,
				MimeType:    skillFileMIME(fileRel),
			},
			handler: func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
				return NewResourceResponseText(fileURI, string(fileContent), skillFileMIME(fileRel)), nil
			},
		}
	}
	if s.skills == nil {
		s.skills = map[string]*skillRegistration{}
	}
	s.skills[b.RootURI()] = &skillRegistration{builder: b, entry: entry}
}

type skillRegistration struct {
	builder *SkillBuilder
	entry   *Skill
}

// RegisterSkill adds a skill to the server: its files become readable
// resources and it appears in skills/list. Registering the first skill also
// declares the skills extension capability. Panics when the skill has no
// SKILL.md (required by the Agent Skills specification).
func (s *Server) RegisterSkill(skill *SkillBuilder) {
	if skill == nil {
		return
	}
	if _, ok := skill.files["SKILL.md"]; !ok {
		panic(fmt.Sprintf("mcp: skill %q must contain SKILL.md at its root (Agent Skills specification)", skill.name))
	}

	s.mu.Lock()
	// Remove a previous registration of the same root first so re-register
	// does not leak resources.
	s.unregisterSkillLocked(skill.RootURI())
	s.registerSkillLocked(skill)
	// Advertise the extension so capability-gating clients surface skills.
	if s.extensionCapabilities == nil {
		s.extensionCapabilities = map[string]any{}
	}
	if _, ok := s.extensionCapabilities[SkillsExtensionID]; !ok {
		s.extensionCapabilities[SkillsExtensionID] = map[string]any{}
	}
	s.mu.Unlock()
	s.NotifyResourcesChanged()
}

// UnregisterSkill removes a skill by name, including its resources.
func (s *Server) UnregisterSkill(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for root := range s.skills {
		if root == "skill://"+name || strings.HasSuffix(root, "/"+name) {
			s.unregisterSkillLocked(root)
			return true
		}
	}
	return false
}

// unregisterSkillLocked removes one registration and its resources.
func (s *Server) unregisterSkillLocked(root string) {
	reg, ok := s.skills[root]
	if !ok {
		return
	}
	for _, res := range reg.entry.Resources {
		delete(s.resources, res.URI)
	}
	delete(s.skills, root)
}

// ListSkills returns the registered skills, sorted by URI.
func (s *Server) ListSkills() []Skill {
	s.mu.RLock()
	entries := make([]Skill, 0, len(s.skills))
	for _, reg := range s.skills {
		entries = append(entries, *reg.entry)
	}
	s.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].URI < entries[j].URI })
	return entries
}

// GetSkill returns one skill's entry by URI: the SKILL.md URI or the root
// directory URI (no trailing slash). ok is false for a URI the server does
// not serve, which callers report as -32602 per the extension.
func (s *Server) GetSkill(uri string) (*Skill, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if reg, ok := s.skills[uri]; ok {
		return reg.entry, true
	}
	if reg, ok := s.skills[strings.TrimSuffix(uri, "/SKILL.md")]; ok {
		return reg.entry, true
	}
	return nil, false
}

// ListSkillsWithContext returns the statically-registered skills plus those
// from any [SkillProvider]s on ctx, sorted by URI — the listing behind
// skills/list. Static registrations win URI collisions; provider errors are
// skipped, matching the resources-from-providers convention (one failing
// provider must not blank the whole listing).
func (s *Server) ListSkillsWithContext(ctx context.Context) []Skill {
	entries := s.ListSkills()
	providers := GetSkillProviders(ctx)
	if len(providers) == 0 {
		return entries
	}
	seen := make(map[string]bool, len(entries))
	for i := range entries {
		seen[entries[i].URI] = true
	}
	for _, provider := range providers {
		skills, err := provider.ListSkills(ctx)
		if err != nil || skills == nil {
			continue
		}
		for _, skill := range skills {
			if !seen[skill.URI] {
				entries = append(entries, skill)
				seen[skill.URI] = true
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].URI < entries[j].URI })
	return entries
}

// GetSkillWithContext resolves one skill's entry by URI — the resolver
// behind skills/get: the static registry first, then any [SkillProvider]s on
// ctx. Both the SKILL.md URI and the root directory URI are accepted,
// per GetSkill.
func (s *Server) GetSkillWithContext(ctx context.Context, uri string) (*Skill, bool) {
	if skill, ok := s.GetSkill(uri); ok {
		return skill, true
	}
	for _, provider := range GetSkillProviders(ctx) {
		skills, err := provider.ListSkills(ctx)
		if err != nil || skills == nil {
			continue
		}
		for i := range skills {
			if skills[i].URI == uri || strings.TrimSuffix(skills[i].URI, "/SKILL.md") == uri {
				return &skills[i], true
			}
		}
	}
	return nil, false
}

// Client-side skills API.

// ListSkills fetches the server's skills/list.
func (c *Client) ListSkills(ctx context.Context) ([]Skill, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	req := MCPRequest{JSONRPC: "2.0", ID: "list-skills", Method: "skills/list"}
	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("list skills failed: %w", err)
	}
	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	var parsed struct {
		Skills []Skill `json:"skills"`
	}
	if err := decodeResult(resp.Result, &parsed, "skills response"); err != nil {
		return nil, err
	}
	return parsed.Skills, nil
}

// GetSkill fetches one skill's entry by URI via skills/get. The entry
// carries metadata and digests; read the content itself with ReadResource
// on any listed resource URI.
func (c *Client) GetSkill(ctx context.Context, uri string) (*Skill, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "get-skill",
		Method:  "skills/get",
		Params:  map[string]any{"uri": uri},
	}
	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("get skill failed: %w", err)
	}
	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	var parsed struct {
		Skill *Skill `json:"skill"`
	}
	if err := decodeResult(resp.Result, &parsed, "skill response"); err != nil {
		return nil, err
	}
	if parsed.Skill == nil {
		return nil, fmt.Errorf("get skill: empty result for %q", uri)
	}
	return parsed.Skill, nil
}
