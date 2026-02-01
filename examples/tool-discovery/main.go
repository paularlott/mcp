package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/paularlott/mcp"
)

func main() {
	server := mcp.NewServer("tool-discovery-example", "1.0.0")

	// Set helpful instructions for the LLM
	server.SetInstructions(`This server has many specialized tools available.
Use tool_search to discover tools for specific tasks.
The search results include full schemas, then use execute_tool to call the tool.`)

	// =========================================================================
	// 1. Register a native tool (visible in tools/list, not searchable)
	// =========================================================================

	server.RegisterTool(
		mcp.NewTool("help", "Get help and guidance on using this server"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText(`Welcome! This server uses tool discovery to reduce context usage.

To find and use tools:
1. Use 'tool_search' with keywords like "database", "email", "pdf", etc.
   The results include full schemas with parameter information.
2. Use 'execute_tool' to call the discovered tool

Example workflow:
- tool_search(query="send email") -> finds "send_email" tool with its schema
- execute_tool(name="send_email", arguments={"to":"user@example.com", "subject":"Hello", "body":"..."})`), nil
		},
	)

	// =========================================================================
	// 2. Register discoverable tools (searchable via tool_search, not in tools/list)
	// =========================================================================

	// Database tools
	server.RegisterTool(
		mcp.NewTool("sql_query", "Execute SQL queries against the database",
			mcp.String("query", "SQL query to execute", mcp.Required()),
			mcp.String("database", "Database name (default: main)"),
		).Discoverable("database", "sql", "query", "select", "insert", "update", "delete"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			query, _ := req.String("query")
			db := req.StringOr("database", "main")
			return mcp.NewToolResponseText(fmt.Sprintf("Executed on %s: %s\n(simulated result)", db, query)), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("database_backup", "Create a backup of the database",
			mcp.String("database", "Database to backup", mcp.Required()),
			mcp.String("destination", "Backup destination path"),
		).Discoverable("database", "backup", "export", "save"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			db, _ := req.String("database")
			dest := req.StringOr("destination", "/backups")
			return mcp.NewToolResponseText(fmt.Sprintf("Backup of %s created at %s", db, dest)), nil
		},
	)

	// Email tools
	server.RegisterTool(
		mcp.NewTool("send_email", "Send an email message",
			mcp.String("to", "Recipient email address", mcp.Required()),
			mcp.String("subject", "Email subject", mcp.Required()),
			mcp.String("body", "Email body content", mcp.Required()),
			mcp.StringArray("cc", "CC recipients"),
			mcp.Boolean("html", "Send as HTML email"),
		).Discoverable("email", "mail", "send", "notification", "smtp", "message"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			to, _ := req.String("to")
			subject, _ := req.String("subject")
			return mcp.NewToolResponseText(fmt.Sprintf("Email sent to %s: %s", to, subject)), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("list_emails", "List emails from inbox",
			mcp.Number("limit", "Maximum number of emails to return"),
			mcp.String("folder", "Folder to list (inbox, sent, drafts)"),
			mcp.Boolean("unread_only", "Only show unread emails"),
		).Discoverable("email", "mail", "inbox", "list", "read"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			folder := req.StringOr("folder", "inbox")
			limit := req.IntOr("limit", 10)
			return mcp.NewToolResponseText(fmt.Sprintf("Listing %d emails from %s (simulated)", limit, folder)), nil
		},
	)

	// Document tools
	server.RegisterTool(
		mcp.NewTool("create_pdf", "Generate a PDF document",
			mcp.String("title", "Document title", mcp.Required()),
			mcp.String("content", "Document content (markdown supported)", mcp.Required()),
			mcp.String("output", "Output filename"),
		).Discoverable("pdf", "document", "export", "generate", "report"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			title, _ := req.String("title")
			output := req.StringOr("output", "document.pdf")
			return mcp.NewToolResponseText(fmt.Sprintf("PDF '%s' created: %s", title, output)), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("convert_document", "Convert documents between formats",
			mcp.String("input", "Input file path", mcp.Required()),
			mcp.String("output_format", "Output format (pdf, docx, html, md)", mcp.Required()),
		).Discoverable("convert", "document", "pdf", "docx", "html", "markdown", "transform"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			input, _ := req.String("input")
			format, _ := req.String("output_format")
			return mcp.NewToolResponseText(fmt.Sprintf("Converted %s to %s format", input, format)), nil
		},
	)

	// DevOps tools
	server.RegisterTool(
		mcp.NewTool("kubernetes_deploy", "Deploy application to Kubernetes",
			mcp.String("manifest", "Kubernetes manifest YAML", mcp.Required()),
			mcp.String("namespace", "Target namespace"),
			mcp.Boolean("dry_run", "Perform a dry run without applying"),
		).Discoverable("kubernetes", "k8s", "deploy", "container", "orchestration", "devops"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			ns := req.StringOr("namespace", "default")
			dryRun := req.BoolOr("dry_run", false)
			action := "Deployed"
			if dryRun {
				action = "Dry run completed"
			}
			return mcp.NewToolResponseText(fmt.Sprintf("%s to namespace: %s", action, ns)), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("docker_build", "Build a Docker image",
			mcp.String("dockerfile", "Path to Dockerfile", mcp.Required()),
			mcp.String("tag", "Image tag", mcp.Required()),
			mcp.Boolean("push", "Push to registry after build"),
		).Discoverable("docker", "container", "build", "image", "devops"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			tag, _ := req.String("tag")
			push := req.BoolOr("push", false)
			result := fmt.Sprintf("Built image: %s", tag)
			if push {
				result += " (pushed to registry)"
			}
			return mcp.NewToolResponseText(result), nil
		},
	)

	// Analytics tools
	server.RegisterTool(
		mcp.NewTool("analyze_data", "Perform statistical analysis on data",
			mcp.String("dataset", "Dataset name or path", mcp.Required()),
			mcp.StringArray("columns", "Columns to analyze"),
			mcp.String("operation", "Analysis type (mean, median, std, correlation)"),
		).Discoverable("analytics", "statistics", "data", "analysis", "pandas", "numpy"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			dataset, _ := req.String("dataset")
			op := req.StringOr("operation", "summary")
			return mcp.NewToolResponseText(fmt.Sprintf("Analysis (%s) of %s: (simulated results)", op, dataset)), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("create_chart", "Generate charts and visualizations",
			mcp.String("type", "Chart type (bar, line, pie, scatter)", mcp.Required()),
			mcp.String("data", "Data in JSON format", mcp.Required()),
			mcp.String("title", "Chart title"),
		).Discoverable("chart", "graph", "visualization", "plot", "matplotlib"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			chartType, _ := req.String("type")
			title := req.StringOr("title", "Chart")
			return mcp.NewToolResponseText(fmt.Sprintf("Created %s chart: %s", chartType, title)), nil
		},
	)

	// Script-based tools
	server.RegisterTool(
		mcp.NewTool("run_backup_script", "Run the automated backup script",
			mcp.String("target", "Backup target (database, files, all)"),
			mcp.Boolean("compress", "Compress backup files"),
		).Discoverable("backup", "script", "database", "files", "automation"),
		handleRunBackupScript,
	)

	server.RegisterTool(
		mcp.NewTool("cleanup_logs", "Clean up old log files to free disk space",
			mcp.Number("days", "Delete logs older than this many days (default: 30)"),
			mcp.String("log_type", "Type of logs to clean (application, access, error, all)"),
			mcp.Boolean("dry_run", "Show what would be deleted without actually deleting"),
		).Discoverable("cleanup", "logs", "disk", "space", "maintenance", "script"),
		handleCleanupLogs,
	)

	server.RegisterTool(
		mcp.NewTool("system_health_check", "Run comprehensive system health checks",
			mcp.StringArray("checks", "Specific checks to run (disk, memory, cpu, network)"),
			mcp.Boolean("verbose", "Include detailed metrics in the output"),
		).Discoverable("health", "monitoring", "status", "check", "disk", "memory", "cpu", "network", "system", "diagnostics"),
		handleHealthCheck,
	)

	// =========================================================================
	// 3. Set up HTTP handler
	// =========================================================================

	http.HandleFunc("/mcp", server.HandleRequest)

	// Print info about registered tools
	tools := server.ListTools()
	fmt.Printf("Server starting with %d visible tools:\n", len(tools))
	for _, t := range tools {
		fmt.Printf("  - %s: %s\n", t.Name, t.Description)
	}
	fmt.Println("\nSearchable tools are discoverable via tool_search")
	fmt.Println("Listening on http://localhost:8088/mcp")

	log.Fatal(http.ListenAndServe(":8088", nil))
}

// =============================================================================
// Script tool handlers
// =============================================================================

func handleRunBackupScript(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	target := req.StringOr("target", "all")
	compress := req.BoolOr("compress", true)

	result := fmt.Sprintf("Running backup script...\nTarget: %s\nCompress: %v\n\n", target, compress)
	result += "Backup completed successfully!\n"
	result += "- Database: backed up (12.3 GB)\n"
	result += "- Files: backed up (45.6 GB)\n"
	result += "- Location: /backups/2024-12-10/"

	return mcp.NewToolResponseText(result), nil
}

func handleCleanupLogs(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	days := req.IntOr("days", 30)
	logType := req.StringOr("log_type", "all")
	dryRun := req.BoolOr("dry_run", false)

	action := "Deleted"
	if dryRun {
		action = "Would delete"
	}

	result := fmt.Sprintf("Cleaning up %s logs older than %d days...\n\n", logType, days)
	result += fmt.Sprintf("%s:\n", action)
	result += "- application.log.1-5 (234 MB)\n"
	result += "- access.log.1-10 (1.2 GB)\n"
	result += "- error.log.1-3 (45 MB)\n\n"
	result += "Total space freed: 1.48 GB"

	return mcp.NewToolResponseText(result), nil
}

func handleHealthCheck(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	verbose := req.BoolOr("verbose", false)
	checks := req.StringSliceOr("checks", []string{"disk", "memory", "cpu", "network"})

	result := fmt.Sprintf("Running health checks: %v\n\n", checks)
	result += "✓ Disk: OK (45%% used, 234 GB free)\n"
	result += "✓ Memory: OK (62%% used, 12.4 GB free)\n"
	result += "✓ CPU: OK (avg load 0.45)\n"
	result += "✓ Network: OK (latency 2ms)\n"

	if verbose {
		result += "\nDetailed metrics:\n"
		result += "- Disk I/O: 45 MB/s read, 23 MB/s write\n"
		result += "- Memory: 8.2 GB cached, 2.1 GB buffers\n"
		result += "- CPU: 4 cores, 8 threads\n"
		result += "- Network: 1 Gbps link, 0 errors"
	}

	return mcp.NewToolResponseText(result), nil
}
