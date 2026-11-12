package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// searchEvalTools is a generic business-application tool set used to judge
// tool_search ranking. Descriptions are deliberately long and detailed, the
// way tools written for LLMs are: a ranking that rewards incidental words in
// long descriptions misranks here, which is the failure this guards against.
func searchEvalTools() []MCPTool {
	tool := func(name, description string, keywords ...string) MCPTool {
		return MCPTool{Name: name, Description: description, Keywords: keywords}
	}
	return []MCPTool{
		tool("list_tickets",
			"List support tickets (tasks, jobs). By default returns only tickets assigned to the current user. Pass a status or priority name to filter, set all_users for everyone's tickets, and use period for tickets created in a date range. Results are newest first (most recently created), so for the last ticket created call it once with limit=1. To count tickets or break them down (who raised the most, how many per project), use summarise_tickets instead of counting pages of this list. Always include the total count in your response.",
			"ticket", "tickets", "task", "tasks", "job", "assigned", "my", "priority", "created", "raised", "latest", "newest", "recent", "oldest"),
		tool("summarise_tickets",
			"Count support tickets, optionally broken down - for questions such as how many tickets were raised last month, who raised the most tickets this quarter, how many urgent tickets are open per project, how many tickets did we complete in October. Reads every matching ticket, not one page. Set group_by to total for one figure, or created_by, assigned_to, project, status, priority, day or month for a breakdown. Completed tickets are counted by default. Always quote figures against the period the tool returns and say when the figures are incomplete.",
			"ticket", "tickets", "count", "how many", "summary", "breakdown", "raised", "created", "completed", "most", "per user", "per project", "statistics"),
		tool("get_ticket",
			"Get detailed information about a single support ticket when you have its ID, including the description, status, comments, time entries, files and metadata. Use this when the user mentions a specific ticket number.",
			"ticket", "details", "show", "info"),
		tool("reply_to_ticket",
			"Add a reply or comment to an existing ticket, optionally changing its status, passing it on, copying users in and recording time spent on it. You must already have the ticket ID; if you only have a name, find the ID first with list_tickets or search_records.",
			"reply", "respond", "comment", "note", "ticket", "update", "log time", "status"),
		tool("create_ticket",
			"Create a new support ticket from a template. Use when the user asks to raise, open, log or create a new ticket. A template is required; list_ticket_templates gives the templates available.",
			"create", "new", "raise", "open", "ticket", "template"),
		tool("list_ticket_templates",
			"List the ticket templates the current user can create from, with each template's name and identifier.",
			"template", "templates", "ticket", "create"),
		tool("ticket_dashboard",
			"Show the user an interactive dashboard of tickets grouped by status or priority, with animated bar charts, headline figures and totals. Defaults to the current user's own tickets. For counts without a chart, or other breakdowns, use summarise_tickets.",
			"dashboard", "ticket", "report", "chart", "graph", "status", "priority", "visualise", "visualize", "manager"),
		tool("list_deals",
			"List sales deals (opportunities, leads in the sales pipeline) - for questions such as show my open deals, which deals did Sam win last month, what is expected to close this month. Filter by period on the closing, created or expected closing date, owner, status, outcome, customer or name. To total sales values or count deals, use summarise_deals rather than adding up pages of this list. Results are newest first, so for the latest deal call it once with limit=1. Always include the total count in your response.",
			"deal", "deals", "opportunity", "opportunities", "sales", "pipeline", "lead", "won", "lost", "forecast", "prospect", "latest", "newest"),
		tool("summarise_deals",
			"Count sales deals and total their value, optionally broken down - for questions such as how much did we sell in October, who won the most sales this quarter, what is the pipeline expected to close next month by owner. Reads every matching deal, not one page. Set group_by to total for one figure, or owner, status, day or month for a breakdown. Always say exactly what was counted and quote figures against the period it returns.",
			"deal", "deals", "opportunity", "opportunities", "sales", "revenue", "won", "pipeline", "total", "how much", "breakdown", "top seller", "by owner", "forecast", "summary"),
		tool("list_customers",
			"Get a paginated list of customer records with company name, contact details and account types. Filter by customer type, search by name or email, or both. Results are in company name order; use sort newest for the latest customers added. Always include the total count in your response.",
			"customer", "customers", "client", "supplier", "contact", "accounts", "companies", "search", "find customer"),
		tool("list_projects",
			"List projects with their status and customer. Search by project code, project name or customer name, and filter by status. Results are in project name order.",
			"project", "projects", "job", "engagement", "status"),
		tool("list_users",
			"Get a paginated list of company users (staff). Returns user ID, full name and email only. Optionally search by name or email. Use this to find a staff member's user ID or email address before passing them to a tool that takes a user.",
			"user", "users", "staff", "employees", "team", "email"),
		tool("list_timesheets",
			"List or summarise recorded time (timesheet entries, logged hours). Use this when the user asks what someone worked on, how much time was recorded, or where time went - for example what did I do last week or break down last month's time by user. Set group_by to get totals instead of rows.",
			"time", "timesheet", "hours", "recorded", "logged", "worked", "effort", "breakdown", "summary"),
		tool("list_attendance",
			"List or summarise attendance - when people clocked in and out, and how many hours they were clocked on. Use this for clocked hours, who clocked in, start and finish times, overtime or breaks.",
			"attendance", "clocked", "clock in", "clock out", "shift", "overtime", "break", "who was in"),
		tool("list_leave",
			"List who is on leave in a period, and how much leave was taken, from approved leave requests. Use it for who is away next week or how many days of leave did the team take this year.",
			"leave", "holiday", "vacation", "away", "absence", "off"),
		tool("list_reports",
			"List the reports that can be run, each with its saved filters and the parameters it accepts. Use this when the user asks what reports are available, and always before run_report.",
			"report", "reports", "available reports", "saved filter", "summary"),
		tool("run_report",
			"Run one report and return its summary figures - the totals and counts from the top of the report screen. Call list_reports first for the report name and parameters. Quote every figure against the period the tool returns.",
			"report", "run report", "reporting", "summary", "figures", "totals"),
		tool("get_report_data",
			"Return the rows behind one of a report's figures, where the report offers them. Call it with the same report, filter and overrides as run_report.",
			"report", "report data", "rows", "dataset", "drill down", "details"),
		tool("chart_maker",
			"Render a pie, bar, or line chart from labelled numeric data and show it to the user as an interactive chart.",
			"chart", "graph", "visualize", "visualise", "plot", "pie", "bar", "line"),
		tool("run_script",
			"Write and run a short read-only script over the application's data, for questions no other tool answers - for example which user generated the most sales in October or how many tickets did each project open per month this year. Prefer a dedicated tool or run_report when one fits. Aggregate in the script and return totals or the top rows rather than every record.",
			"analysis", "script", "python", "code", "calculate", "aggregate", "total", "count", "group by", "top", "most", "ranking", "breakdown", "trend", "custom report"),
		tool("script_docs",
			"Documentation for run_script: the rules, the libraries a script reads data with, and worked examples. With a library name, that library's functions and parameters.",
			"script docs", "library docs", "how to write a script"),
		tool("search_records",
			"Full-text search across tickets, their replies and video transcripts. Use this when the user wants to search or find tickets or what was said in them by keyword, name or phrase - not when they already have a specific ID.",
			"search", "find", "query", "lookup", "ticket", "reply", "transcript", "video"),
		tool("memory_recall",
			"Search the memories stored for this user about their preferences and past conversations. Omit the query for recent context.",
			"memory", "remember", "recall", "preference"),
		tool("memory_remember",
			"Store one fact, preference, event or note about the user for future conversations.",
			"memory", "remember", "store", "preference", "note"),
		tool("list_invoices",
			"List invoices and credit notes with their totals, due dates and whether they are paid. Filter by period on the invoice, due or paid date, customer, salesperson, unpaid or overdue only. Use summarise_invoices to total them.",
			"invoice", "invoices", "billing", "unpaid", "overdue", "paid", "credit note"),
		tool("summarise_invoices",
			"Total invoices, optionally broken down by customer, salesperson, status or month - for how much did we invoice last month or what is overdue by customer.",
			"invoice", "invoices", "revenue", "total", "outstanding", "overdue", "breakdown", "how much"),
		tool("list_document_templates",
			"List the document templates generated documents are made from (quotes, invoices, letters). Returns each template's id, title and type, not its content.",
			"document template", "template", "quote template", "invoice template", "letter", "pdf"),
		tool("send_email",
			"Send an email to a contact using one of the email templates, with optional attachments. Confirm the recipient and content with the user first.",
			"email", "send", "mail", "message"),
	}
}

type searchEvalCase struct {
	query string
	// Each tool listed must appear within the first topN results.
	want []string
	topN int
}

// searchEvalCases are realistic queries an assistant writes for tool_search,
// with the tools that must rank near the top. The first few are taken from
// misrankings seen in practice: generic words (count, created, most) in a long
// description outranking the one word that names the subject, and a multi-intent
// query burying the tool for one of its intents.
func searchEvalCases() []searchEvalCase {
	return []searchEvalCase{
		{"opportunities created count summarise who created most", []string{"summarise_deals"}, 2},
		{"visualise opportunities by value", []string{"chart_maker", "list_deals"}, 4},
		{"opportunities list chart", []string{"chart_maker", "list_deals"}, 3},
		{"last ticket created most recent", []string{"list_tickets"}, 2},
		{"get ticket by id details", []string{"get_ticket"}, 1},
		{"search tickets sort created date latest", []string{"list_tickets"}, 3},
		{"who is on leave next week", []string{"list_leave"}, 1},
		{"how many hours did the team log last month", []string{"list_timesheets"}, 2},
		{"top selling customers revenue this year", []string{"summarise_deals"}, 3},
		{"overdue invoices by customer", []string{"summarise_invoices", "list_invoices"}, 3},
		{"plot sales by month", []string{"chart_maker", "summarise_deals"}, 4},
		{"opportunity pipeline forecast", []string{"summarise_deals", "list_deals"}, 3},
		{"remember that I prefer short answers", []string{"memory_remember"}, 2},
		{"find staff member email", []string{"list_users"}, 2},
		{"reply to ticket and log time", []string{"reply_to_ticket"}, 1},
		{"what reports are available", []string{"list_reports"}, 2},
		{"summarising tickets per project", []string{"summarise_tickets"}, 1},
		{"visualizing ticket statuses", []string{"ticket_dashboard"}, 2},
		{"chart_maker", []string{"chart_maker"}, 1},
	}
}

func runSearchEval(t *testing.T) (failures []string) {
	t.Helper()
	r := newInternalRegistry()
	tools := searchEvalTools()
	for _, c := range searchEvalCases() {
		results := r.SearchWithAdditionalTools(context.Background(), c.query, 10, nil, tools)
		rank := map[string]int{}
		for i, res := range results {
			rank[res.Name] = i + 1
		}
		for _, want := range c.want {
			if pos, ok := rank[want]; !ok || pos > c.topN {
				var top []string
				for i, res := range results {
					if i == 5 {
						break
					}
					top = append(top, fmt.Sprintf("%s %.2f", res.Name, res.Score))
				}
				failures = append(failures, fmt.Sprintf("%q: %s at rank %d, want within %d; top: %s",
					c.query, want, pos, c.topN, strings.Join(top, ", ")))
			}
		}
	}
	return failures
}

// TestToolSearchRanksRelevantToolsFirst is the tool_search ranking evaluation:
// every case must put its tools within the top N.
func TestToolSearchRanksRelevantToolsFirst(t *testing.T) {
	failures := runSearchEval(t)
	for _, f := range failures {
		t.Error(f)
	}
	if len(failures) > 0 {
		t.Logf("%d of the evaluation's expectations failed", len(failures))
	}
}
