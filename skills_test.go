package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

	// Missing SKILL.md is rejected with an error (callers reading skills
	// from files skip such entries with a warning).
	NewSkill("broken").File("OTHER.md", []byte("x"))
	if err := s.RegisterSkill(NewSkill("broken")); err == nil {
		t.Fatal("registering a skill without SKILL.md must error")
	}
}

// Spec conformance details: the frontmatter name must equal the final path
// segment of the skill URI; the SKILL.md resource descriptor carries the
// frontmatter name and description; Modern-era skills/get carries caching
// hints.
func TestSkillsSpecDetails(t *testing.T) {
	s := NewServer("s", "1")
	if err := s.RegisterSkill(NewSkill("code-review").
		File("SKILL.md", []byte("---\nname: code-review\ndescription: Review changesets\n---\nBody."))); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Frontmatter name != directory name is rejected, before any state
	// changes: a failed registration must not disturb the existing one.
	if err := s.RegisterSkill(NewSkill("mismatch").
		File("SKILL.md", []byte("---\nname: other-name\n---\nBody."))); err == nil {
		t.Fatal("name/path mismatch must error at registration")
	}
	if skills := s.ListSkills(); len(skills) != 1 {
		t.Fatalf("failed registration must not change state: %+v", skills)
	}

	// The SKILL.md resource descriptor carries the frontmatter identity.
	resource, ok := s.resources["skill://code-review/SKILL.md"]
	if !ok {
		t.Fatal("SKILL.md must be registered as a resource")
	}
	if resource.descriptor.Name != "code-review" || resource.descriptor.Description != "Review changesets" {
		t.Fatalf("descriptor = %+v, want frontmatter name and description", resource.descriptor)
	}

	// Modern-era caching hints cover skills/get too (ttlMs/cacheScope are
	// REQUIRED on it, not just on skills/list).
	if _, _, ok := cacheHintsFor("skills/get"); !ok {
		t.Fatal("cacheHintsFor(skills/get) must apply")
	}
	if _, _, ok := cacheHintsFor("skills/list"); !ok {
		t.Fatal("cacheHintsFor(skills/list) must apply")
	}
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

type fakeSkillProvider struct {
	skills []Skill
	err    error
}

func (p *fakeSkillProvider) ListSkills(ctx context.Context) ([]Skill, error) {
	return p.skills, p.err
}

// SkillProviders on the request context join skills/list and skills/get
// alongside the static registry, which wins URI collisions; a failing
// provider is skipped, not fatal.
func TestSkillProvidersOnContext(t *testing.T) {
	s := NewServer("s", "1")
	s.RegisterSkill(NewSkill("static-skill").
		Description("From the registry").
		File("SKILL.md", []byte("static body")))

	dbSkill := Skill{
		URI:         "skill://db-skill/SKILL.md",
		Frontmatter: map[string]any{"name": "db-skill", "description": "From the database"},
		Resources:   []SkillResource{{URI: "skill://db-skill/SKILL.md", Digest: "sha256:x", Size: 3}},
	}
	colliding := Skill{
		URI:         "skill://static-skill/SKILL.md", // static registry serves this URI already
		Frontmatter: map[string]any{"name": "static-skill", "description": "provider loses"},
	}
	ctx := WithSkillProviders(context.Background(),
		&fakeSkillProvider{skills: []Skill{dbSkill, colliding}},
		&fakeSkillProvider{err: errors.New("backend down")})

	skills := s.ListSkillsWithContext(ctx)
	if len(skills) != 2 {
		t.Fatalf("skills = %d (%+v), want static + provider entry", len(skills), skills)
	}
	if skills[0].URI != "skill://db-skill/SKILL.md" || skills[1].URI != "skill://static-skill/SKILL.md" {
		t.Fatalf("listing = %+v", skills)
	}
	if skills[1].Frontmatter["description"] != "From the registry" {
		t.Fatalf("static registry must win the collision: %+v", skills[1].Frontmatter)
	}

	// skills/get resolves provider entries by SKILL.md URI and root URI.
	if e, ok := s.GetSkillWithContext(ctx, "skill://db-skill/SKILL.md"); !ok || e.URI != dbSkill.URI {
		t.Fatalf("get by entry URI = (%+v, %v)", e, ok)
	}
	if _, ok := s.GetSkillWithContext(ctx, "skill://db-skill"); !ok {
		t.Fatal("get by root URI must work for provider skills")
	}
	if _, ok := s.GetSkillWithContext(ctx, "skill://nope/SKILL.md"); ok {
		t.Fatal("unknown URI must not resolve")
	}

	// The HTTP dispatch carries the request context through: skills/list and
	// skills/get both see the provider.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"skills/list"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.HandleRequest(rec, req)
	if out := rec.Body.String(); !strings.Contains(out, `"uri":"skill://db-skill/SKILL.md"`) {
		t.Fatalf("wire skills/list must include provider skills: %s", out)
	}
}

// Skills federation (WithRemoteSkillsFederation): a registered remote's
// skills are re-published under skill://<ns>/… URIs, the manifest's resource
// URIs are rewritten to the same root (digests untouched — they cover bytes,
// not URIs), skills/get resolves the rewritten forms, and file reads route
// to the owning server with the namespace stripped. Without the option a
// remote contributes no skills at all.
func TestSkillsFederationFromRemoteServer(t *testing.T) {
	remote := NewServer("remote-skills", "1.0")
	remote.RegisterSkill(NewSkill("dashboard-ops").
		Description("Operate dashboards").
		File("SKILL.md", []byte("---\nname: dashboard-ops\ndescription: Operate dashboards\n---\nDo things.")).
		File("references/regions.md", []byte("eu-1, us-1")))
	remoteSrv := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer remoteSrv.Close()

	gateway := NewServer("gateway", "1.0")
	if err := gateway.RegisterRemoteServer(NewClient(remoteSrv.URL, nil, "app"), WithRemoteSkillsFederation()); err != nil {
		t.Fatalf("register: %v", err)
	}

	// The capability is declared even with no statically-registered skills.
	if _, ok := gateway.extensionCapabilities[SkillsExtensionID]; !ok {
		t.Fatal("skills federation must declare io.modelcontextprotocol/skills")
	}

	skills := gateway.ListSkillsWithContext(context.Background())
	if len(skills) != 1 {
		t.Fatalf("skills = %+v, want the federated entry", skills)
	}
	entry := skills[0]
	if entry.URI != "skill://app/dashboard-ops/SKILL.md" {
		t.Fatalf("entry URI = %q, want the namespace inserted as the first path segment", entry.URI)
	}
	if entry.Frontmatter["name"] != "dashboard-ops" || entry.Frontmatter["description"] != "Operate dashboards" {
		t.Fatalf("frontmatter must pass through verbatim: %+v", entry.Frontmatter)
	}
	if len(entry.Resources) != 2 {
		t.Fatalf("resources = %+v", entry.Resources)
	}
	var wantDigest string
	for _, r := range entry.Resources {
		switch r.URI {
		case "skill://app/dashboard-ops/SKILL.md", "skill://app/dashboard-ops/references/regions.md":
		default:
			t.Fatalf("resource URI not rewritten to the federated root: %q", r.URI)
		}
		if strings.HasSuffix(r.URI, "/SKILL.md") {
			wantDigest = r.Digest
		}
	}
	// The digest matches the remote's own bytes: rewriting never touches content.
	remoteEntry, err := NewClient(remoteSrv.URL, nil, "").ListSkills(context.Background())
	if err != nil || len(remoteEntry) != 1 {
		t.Fatalf("remote listing = (%+v, %v)", remoteEntry, err)
	}
	if remoteEntry[0].Resources[0].Digest != wantDigest {
		t.Fatalf("digest changed across the rewrite: %s vs %s", wantDigest, remoteEntry[0].Resources[0].Digest)
	}

	// skills/get by entry URI and by root URI.
	if e, ok := gateway.GetSkillWithContext(context.Background(), "skill://app/dashboard-ops/SKILL.md"); !ok || e.URI != entry.URI {
		t.Fatalf("get by entry URI = (%+v, %v)", e, ok)
	}
	if _, ok := gateway.GetSkillWithContext(context.Background(), "skill://app/dashboard-ops"); !ok {
		t.Fatal("get by root URI must work for federated skills")
	}
	// A URI naming a namespace nothing federates resolves to nothing: no
	// scanning other remotes' listings for a namespace they don't own.
	if _, ok := gateway.GetSkillWithContext(context.Background(), "skill://other/dashboard-ops/SKILL.md"); ok {
		t.Fatal("URI under a non-federated namespace must not resolve")
	}

	// File reads route to the owner with the namespace stripped: both the
	// SKILL.md and a supporting file, whose rewritten URI only exists on the
	// gateway. The response echoes the namespaced URI the client asked for —
	// the remote's envelope names its own stripped URI, which conformance
	// checkers read as no content (regression: MCP Inspector's skill view).
	for uri, want := range map[string]string{
		"skill://app/dashboard-ops/SKILL.md":              "Do things.",
		"skill://app/dashboard-ops/references/regions.md": "eu-1, us-1",
	} {
		resp, err := gateway.ReadResource(context.Background(), uri)
		if err != nil || !strings.Contains(resp.Contents[0].Text, want) {
			t.Fatalf("ReadResource(%s) = (%+v, %v), want %q", uri, resp, err, want)
		}
		if resp.Contents[0].URI != uri {
			t.Fatalf("content URI = %q, want the requested namespaced URI %q", resp.Contents[0].URI, uri)
		}
	}

	// Without the option, the same registration federates no skills.
	plain := NewServer("gateway-plain", "1.0")
	if err := plain.RegisterRemoteServer(NewClient(remoteSrv.URL, nil, "app2")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if skills := plain.ListSkillsWithContext(context.Background()); len(skills) != 0 {
		t.Fatalf("non-federating registration must list no skills: %+v", skills)
	}
	if _, ok := plain.extensionCapabilities[SkillsExtensionID]; ok {
		t.Fatal("capability must not be declared without skills federation")
	}
	// And its reads fall through to the generic fan-out: a namespaced URI
	// naming no federating namespace is tried verbatim (and misses here).
	if _, err := plain.ReadResource(context.Background(), "skill://app2/dashboard-ops/SKILL.md"); err != ErrUnknownResource {
		t.Fatalf("unowned namespaced skill read = %v, want ErrUnknownResource", err)
	}
}

// RemoteProvider (ctx-attached, per-session resolution) re-publishes each
// remote's skills under skill://<ns>/… URIs, caches listings per server, and
// routes namespaced skill reads to the owner alone with the namespace
// stripped — echoing the namespaced URI back in the response.
func TestRemoteProviderSkills(t *testing.T) {
	remote := NewServer("remote-skills", "1.0")
	remote.RegisterSkill(NewSkill("dashboard-ops").
		Description("Operate dashboards").
		File("SKILL.md", []byte("---\nname: dashboard-ops\ndescription: Operate dashboards\n---\nDo things.")))
	remoteSrv := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer remoteSrv.Close()

	provider := NewRemoteProvider(func(ctx context.Context) ([]RemoteProviderConfig, error) {
		return []RemoteProviderConfig{{Name: "app", URL: remoteSrv.URL}}, nil
	})

	skills, err := provider.ListSkills(context.Background())
	if err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills = (%+v, %v)", skills, err)
	}
	if skills[0].URI != "skill://app/dashboard-ops/SKILL.md" {
		t.Fatalf("URI = %q, want the namespace as the first path segment", skills[0].URI)
	}

	// Owner-routed read with the echoed namespaced URI.
	resp, err := provider.ReadResource(context.Background(), "skill://app/dashboard-ops/SKILL.md")
	if err != nil || !strings.Contains(resp.Contents[0].Text, "Do things.") {
		t.Fatalf("namespaced read = (%+v, %v)", resp, err)
	}
	if resp.Contents[0].URI != "skill://app/dashboard-ops/SKILL.md" {
		t.Fatalf("content URI = %q, want the requested namespaced URI", resp.Contents[0].URI)
	}

	// A URI under a namespace the provider does not serve is a miss for the
	// skills routing (it falls through to the generic fan-out, which has no
	// server holding it verbatim either).
	if _, err := provider.ReadResource(context.Background(), "skill://other/x/SKILL.md"); err != ErrUnknownResource {
		t.Fatalf("unowned namespace read = %v, want ErrUnknownResource", err)
	}

	// The listing is cached: a second call serves the same entries without
	// a per-server fetch (the fake server would still answer, so assert via
	// the provider's cache length).
	if skills2, _ := provider.ListSkills(context.Background()); len(skills2) != 1 || provider.skillsCache.len() != 1 {
		t.Fatalf("second listing = %+v (cache entries %d), want the cached entry", skills2, provider.skillsCache.len())
	}
}

// Unregistering a skills-federating remote drops its cached listing: the
// Server-level listing cache outlives the registration, so it is invalidated
// explicitly (a re-registered server under the same namespace never serves
// the previous incarnation's skills).
func TestSkillsFederationCacheInvalidatedOnUnregister(t *testing.T) {
	remote := NewServer("remote-skills", "1.0")
	remote.RegisterSkill(NewSkill("dashboard-ops").
		Description("Operate dashboards").
		File("SKILL.md", []byte("---\nname: dashboard-ops\ndescription: Operate dashboards\n---\nDo things.")))
	remoteSrv := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer remoteSrv.Close()

	gateway := NewServer("gateway", "1.0")
	client := NewClient(remoteSrv.URL, nil, "app")
	if err := gateway.RegisterRemoteServer(client, WithRemoteSkillsFederation()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if skills := gateway.ListSkillsWithContext(context.Background()); len(skills) != 1 {
		t.Fatalf("listing = %+v, want the federated skill", skills)
	}
	if gateway.skillsFedCache.len() != 1 {
		t.Fatalf("listing cache entries = %d, want 1", gateway.skillsFedCache.len())
	}

	gateway.UnregisterRemoteServer(client)
	if skills := gateway.ListSkillsWithContext(context.Background()); len(skills) != 0 {
		t.Fatalf("listing after unregister = %+v, want empty", skills)
	}
	if gateway.skillsFedCache.len() != 0 {
		t.Fatalf("listing cache entries after unregister = %d, want 0", gateway.skillsFedCache.len())
	}
}

// Production-hardening regressions: a skills/get entry is a copy (mutating
// it cannot change the cached listing the next request is served), and
// federation rewrites only skill:// URIs — the spec permits other schemes,
// and prefixing one would build a garbage URI nothing can route.
func TestSkillsFederationHardening(t *testing.T) {
	// Copy-on-get: mutate a returned federated entry, fetch it again, the
	// cached listing must be unchanged.
	remote := NewServer("remote-skills", "1.0")
	remote.RegisterSkill(NewSkill("dashboard-ops").
		Description("Operate dashboards").
		File("SKILL.md", []byte("---\nname: dashboard-ops\ndescription: Operate dashboards\n---\nDo things.")))
	remoteSrv := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer remoteSrv.Close()

	gateway := NewServer("gateway", "1.0")
	if err := gateway.RegisterRemoteServer(NewClient(remoteSrv.URL, nil, "app"), WithRemoteSkillsFederation()); err != nil {
		t.Fatalf("register: %v", err)
	}
	entry, ok := gateway.GetSkillWithContext(context.Background(), "skill://app/dashboard-ops/SKILL.md")
	if !ok {
		t.Fatal("entry must resolve")
	}
	entry.Frontmatter["description"] = "TAMPERED"
	again, _ := gateway.GetSkillWithContext(context.Background(), "skill://app/dashboard-ops/SKILL.md")
	if again.Frontmatter["description"] != "Operate dashboards" {
		t.Fatalf("cached listing was tampered through the returned entry: %+v", again.Frontmatter)
	}

	// Alternate-scheme URIs pass through verbatim.
	if got := federateSkillURI("ns", Skill{URI: "https://example.com/skill.md", Resources: []SkillResource{{URI: "https://example.com/skill.md"}}}); got.URI != "https://example.com/skill.md" || got.Resources[0].URI != "https://example.com/skill.md" {
		t.Fatalf("alternate scheme mangled: %+v", got)
	}
	if got := federateSkillURI("ns", Skill{URI: "skill://a/SKILL.md", Resources: []SkillResource{{URI: "skill://a/SKILL.md"}}}); got.URI != "skill://ns/a/SKILL.md" || got.Resources[0].URI != "skill://ns/a/SKILL.md" {
		t.Fatalf("skill:// rewrite changed: %+v", got)
	}
}
