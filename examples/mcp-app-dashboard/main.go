// Command mcp-app-dashboard is a worked example of the MCP Apps extension
// (SEP-1865, https://github.com/modelcontextprotocol/ext-apps): a tool linked
// to an interactive HTML UI resource that a compliant host renders in a
// sandboxed iframe.
//
// It registers two tools, only one of which links to a ui:// resource
// (dashboard.html — an AlpineJS + Chart.js table/chart/form):
//
//   - sales_report: visible to the model and the app, and linked to the
//     dashboard resource; returns the current sales records as structured
//     content. This is what the model calls to open the dashboard.
//   - add_sale: visible to the app only (_meta.ui.visibility: ["app"]), with
//     no resourceUri of its own — a compliant host still sees it in
//     tools/list (it needs to, to let the view call it) but hides it from the
//     model, and since it's only ever called by a dashboard that's already
//     open, it has no view of its own to render (resourceUri is optional per
//     spec). The dashboard's own "Add Sale" form calls it directly via
//     tools/call, and it returns the updated records so the view can refresh
//     its table and chart without a round trip through the model.
//
// Run it over HTTP (for the bundled host-simulator test harness, or any
// Streamable-HTTP MCP client) or over stdio (the transport real MCP hosts use
// to spawn a local server):
//
//	go run . -http :8091
//	go run . -stdio
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/paularlott/mcp"
)

//go:embed dashboard.html
var dashboardHTML string

const dashboardResourceURI = "ui://sales-dashboard/dashboard"

// salesReportIcon is a small inline bar-chart SVG, illustrating the MCP
// icons convention on the model-visible tool. add_sale deliberately has no
// icon of its own — icons aren't required on every tool, and a plain "app"
// action tool typically doesn't need one.
var salesReportIcon = mcp.Icon{
	Src:      "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'%3E%3Crect x='3' y='12' width='4' height='9' fill='%234f46e5'/%3E%3Crect x='10' y='6' width='4' height='15' fill='%236366f1'/%3E%3Crect x='17' y='9' width='4' height='12' fill='%238b85f6'/%3E%3C/svg%3E",
	MimeType: "image/svg+xml",
}

// SaleRecord is one row of the sales table, also the shape returned as
// structuredContent by both tools so the dashboard can render either
// response identically.
type SaleRecord struct {
	Date    string  `json:"date"`
	Product string  `json:"product"`
	Amount  float64 `json:"amount"`
}

// salesStore is a trivial in-memory dataset guarded by a mutex, standing in
// for a real backend. Both tool handlers read/write it.
type salesStore struct {
	mu      sync.Mutex
	records []SaleRecord
}

func (s *salesStore) all() []SaleRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SaleRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *salesStore) add(r SaleRecord) []SaleRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	out := make([]SaleRecord, len(s.records))
	copy(out, s.records)
	return out
}

func main() {
	httpAddr := flag.String("http", "", "serve Streamable HTTP on this address, e.g. :8091 (mutually exclusive with -stdio)")
	stdio := flag.Bool("stdio", false, "serve stdio instead of HTTP")
	flag.Parse()

	if *httpAddr == "" && !*stdio {
		*httpAddr = ":8091"
	}
	if *httpAddr != "" && *stdio {
		log.Fatal("pass either -http or -stdio, not both")
	}

	server := newServer()

	if *stdio {
		log.Fatal(server.ServeStdio(context.Background()))
	}

	http.HandleFunc("/mcp", server.HandleRequest)
	fmt.Printf("mcp-app-dashboard listening on %s (POST /mcp)\n", *httpAddr)
	log.Fatal(http.ListenAndServe(*httpAddr, nil))
}

// newServer builds the dashboard server: two tools sharing one ui:// resource.
// Factored out of main so tests can exercise it directly (see main_test.go).
func newServer() *mcp.Server {
	store := &salesStore{records: []SaleRecord{
		{Date: "2026-09-01", Product: "Widget", Amount: 120.50},
		{Date: "2026-09-03", Product: "Gadget", Amount: 75.00},
		{Date: "2026-09-05", Product: "Widget", Amount: 45.25},
		{Date: "2026-09-10", Product: "Gizmo", Amount: 210.00},
	}}

	server := mcp.NewServer("mcp-app-dashboard", "1.0.0")

	// Declare that this server offers MCP Apps content, per the SEP-1724
	// extensions capability mechanism. Hosts that don't support it just ignore
	// the declaration and the linked tools behave as plain text tools.
	server.DeclareExtension(mcp.UIAppsExtensionID, map[string]any{
		"mimeTypes": []string{mcp.UIAppMimeType},
	})

	// The ui:// resource: a self-contained HTML5 document. It loads AlpineJS
	// and Chart.js from a CDN, so the CSP metadata below tells a compliant
	// host which external origin to allow — omitting it would leave the host's
	// restrictive same-origin default in place and the scripts would fail to
	// load.
	server.RegisterResource(
		mcp.NewResource(dashboardResourceURI, "Sales Dashboard", "Table, chart and add-sale form for the sales_report tool", mcp.UIAppMimeType),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			return mcp.NewUIResourceResponseText(req.URI(), dashboardHTML, &mcp.UIResourceMeta{
				CSP: &mcp.UICSP{
					ResourceDomains: []string{"https://cdn.jsdelivr.net"},
				},
				PrefersBorder: boolPtr(true),
			}), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("sales_report", "Get the current sales report as a table and chart",
			salesRecordsOutputSchema(),
		).UIResource(dashboardResourceURI).Icons(salesReportIcon),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseStructured(map[string]any{"records": store.all()}), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("add_sale", "Add a sale record (called by the dashboard's own form, not the model)",
			mcp.String("date", "Sale date, YYYY-MM-DD", mcp.Required()),
			mcp.String("product", "Product name", mcp.Required()),
			mcp.Number("amount", "Sale amount", mcp.Required()),
			salesRecordsOutputSchema(),
		).Visibility("app"), // app-only action tool: called by an already-open dashboard, so it has no view of its own to link (resourceUri is optional per spec, and a compliant host hides an "app"-only tool from the model's tool list regardless)
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			date, err := req.String("date")
			if err != nil {
				return nil, err
			}
			product, err := req.String("product")
			if err != nil {
				return nil, err
			}
			amount, err := req.Float("amount")
			if err != nil {
				return nil, err
			}
			records := store.add(SaleRecord{Date: date, Product: product, Amount: amount})
			return mcp.NewToolResponseStructured(map[string]any{"records": records}), nil
		},
	)

	return server
}

func boolPtr(b bool) *bool { return &b }

// salesRecordsOutputSchema declares the outputSchema both tools share: they
// return the same {"records": [...]} shape as structuredContent (via
// mcp.NewToolResponseStructured), so a client can validate it and a UI can
// render it without guessing the shape from examples. Declaring an
// outputSchema is optional per the MCP spec — structuredContent is valid
// without one — but it's the documented, validated way to describe it.
func salesRecordsOutputSchema() mcp.Parameter {
	return mcp.Output(
		mcp.ObjectArray("records", "The sales records after this call",
			mcp.String("date", "Sale date, YYYY-MM-DD", mcp.Required()),
			mcp.String("product", "Product name", mcp.Required()),
			mcp.Number("amount", "Sale amount", mcp.Required()),
			mcp.Required(),
		),
	)
}
