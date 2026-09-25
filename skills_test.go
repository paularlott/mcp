package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The skills extension per SEP-2640 (Final): a skill is an Agent Skills
// directory whose files are ordinary resources under
// skill://<skill-path>/<file-path>; skills/list carries entries with URI,
// frontmatter and per-file digest/size; skills/get returns an entry by URI
// (the SKILL.md URI or the root), erroring -32602 for unknown URIs; content
// is read with resources/read.
func TestSkillsSpecCompliance(t *testing.T) {
	s := NewServer("skills-server", "1.0")
	skillMD := "---\nname: git-workflow\ndescription: Team Git conventions\nversion: 1.2.0\n---\n\nBranch, commit, ship."
	s.RegisterSkill(NewSkill("git-workflow").
		Description("Team Git conventions").
		File("SKILL.md", []byte(skillMD)).
		File("references/FORMS.md", []byte("Form guide.")))
	s.RegisterSkill(NewSkill("refunds").Prefix("acme", "billing").
		Description("Refund handling").
		File("SKILL.md", []byte("refund skill")))

	// Extension capability declared.
	if _, ok := s.extensionCapabilities[SkillsExtensionID]; !ok {
		t.Fatal("registering a skill must declare io.modelcontextprotocol/skills")
	}

	// skills/list: entries with the spec's exact shape.
	skills := s.ListSkills()
	if len(skills) != 2 {
		t.Fatalf("skills = %d, want 2", len(skills))
	}
	gw := skills[1] // sorted by URI: skill://acme/... first, git-workflow second
	if gw.URI != "skill://git-workflow/SKILL.md" {
		t.Fatalf("entry URI = %q", gw.URI)
	}
	if gw.Frontmatter["name"] != "git-workflow" || gw.Frontmatter["description"] != "Team Git conventions" {
		t.Fatalf("frontmatter = %+v", gw.Frontmatter)
	}
	if gw.Frontmatter["version"] != "1.2.0" {
		t.Fatalf("frontmatter = %+v", gw.Frontmatter)
	}
	if len(gw.Resources) != 2 {
		t.Fatalf("resources = %+v", gw.Resources)
	}
	sum := sha256.Sum256([]byte(skillMD))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	found := false
	for _, r := range gw.Resources {
		if r.URI == "skill://git-workflow/SKILL.md" {
			found = true
			if r.Digest != wantDigest || r.Size != int64(len(skillMD)) {
				t.Fatalf("SKILL.md resource = %+v, want digest %s size %d", r, wantDigest, len(skillMD))
			}
		}
	}
	if !found {
		t.Fatal("SKILL.md not in resources")
	}

	// Prefixed skill's final path segment equals its name.
	if s.ListSkills()[0].URI != "skill://acme/billing/refunds/SKILL.md" {
		t.Fatalf("prefixed URI = %q", s.ListSkills()[0].URI)
	}

	// skills/get by SKILL.md URI and by root URI; unknown URI fails.
	if e, ok := s.GetSkill("skill://git-workflow/SKILL.md"); !ok || e.URI != gw.URI {
		t.Fatalf("get by entry URI = (%+v, %v)", e, ok)
	}
	if _, ok := s.GetSkill("skill://git-workflow"); !ok {
		t.Fatal("get by root URI must work")
	}
	if _, ok := s.GetSkill("skill://nope/SKILL.md"); ok {
		t.Fatal("unknown URI must not resolve")
	}

	// Files are resources, readable via resources/read.
	resp, err := s.ReadResource(context.Background(), "skill://git-workflow/references/FORMS.md")
	if err != nil || !strings.Contains(resp.Contents[0].Text, "Form guide.") {
		t.Fatalf("resources/read of a skill file = (%+v, %v)", resp, err)
	}

	// Missing SKILL.md is a programmer error.
	defer func() { recover() }()
	NewSkill("broken").File("OTHER.md", []byte("x"))
	s.RegisterSkill(NewSkill("broken"))
	t.Fatal("registering a skill without SKILL.md must panic")
}

// Wire round trip: HTTP skills/list + skills/get, and the client API.
func TestSkillsWireRoundTrip(t *testing.T) {
	s := NewServer("skills-server", "1.0")
	s.RegisterSkill(NewSkill("code-review").
		Description("How to review").
		File("SKILL.md", []byte("Read the diff twice.")))

	do := func(body string) string {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.HandleRequest(rec, req)
		return rec.Body.String()
	}

	out := do(`{"jsonrpc":"2.0","id":1,"method":"skills/list"}`)
	if !strings.Contains(out, `"uri":"skill://code-review/SKILL.md"`) ||
		!strings.Contains(out, `"frontmatter"`) || !strings.Contains(out, `"digest":"sha256:`) {
		t.Fatalf("skills/list shape: %s", out)
	}
	out = do(`{"jsonrpc":"2.0","id":2,"method":"skills/get","params":{"uri":"skill://code-review/SKILL.md"}}`)
	if !strings.Contains(out, `"skill"`) || !strings.Contains(out, `"resources"`) {
		t.Fatalf("skills/get shape: %s", out)
	}
	out = do(`{"jsonrpc":"2.0","id":3,"method":"skills/get","params":{"uri":"skill://nope/SKILL.md"}}`)
	if !strings.Contains(out, "-32602") {
		t.Fatalf("unknown skill must be -32602: %s", out)
	}

	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()
	client := NewClient(ts.URL, nil, "")
	skills, err := client.ListSkills(context.Background())
	if err != nil || len(skills) != 1 || skills[0].Frontmatter["name"] != "code-review" {
		t.Fatalf("client ListSkills = (%+v, %v)", skills, err)
	}
	entry, err := client.GetSkill(context.Background(), "skill://code-review/SKILL.md")
	if err != nil || len(entry.Resources) != 1 {
		t.Fatalf("client GetSkill = (%+v, %v)", entry, err)
	}
	// Content via resources/read, per spec.
	res, err := client.ReadResource(context.Background(), "skill://code-review/SKILL.md")
	if err != nil || !strings.Contains(res.Contents[0].Text, "Read the diff twice.") {
		t.Fatalf("client ReadResource = (%+v, %v)", res, err)
	}
}

// The listing's frontmatter must match the served SKILL.md verbatim —
// conformance checkers (e.g. MCP Inspector) flag any divergence, such as a
// listing that nests version under "metadata" while the file declares it
// top-level.
func TestSkillsFrontmatterMatchesServedFile(t *testing.T) {
	s := NewServer("s", "1")
	md := "---\nname: code-review\ndescription: Review changesets\nversion: 1.0.0\nlicense: MIT\n---\n\nBody."
	s.RegisterSkill(NewSkill("code-review").
		Description("fallback only").
		Metadata("version", "9.9.9"). // must NOT beat the file's own fields
		File("SKILL.md", []byte(md)))

	skills := s.ListSkills()
	if len(skills) != 1 {
		t.Fatalf("skills = %d", len(skills))
	}
	fm := skills[0].Frontmatter
	for k, want := range map[string]string{
		"name":        "code-review",
		"description": "Review changesets",
		"version":     "1.0.0",
		"license":     "MIT",
	} {
		if got, _ := fm[k].(string); got != want {
			t.Errorf("frontmatter[%q] = %v, want %q", k, fm[k], want)
		}
	}
	if _, nested := fm["metadata"]; nested {
		t.Errorf("builder Metadata must not leak into a verbatim listing: %+v", fm)
	}
}
