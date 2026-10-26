package archtest

import (
	"bufio"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// skeleton is every identifier other packages build on, by package; "T.M"
// is a field or method M of type T.
var skeleton = map[string][]string{
	"internal/slack": {"TokenSource", "Client", "Client.Call", "Client.OnRateLimited", "Client.RequestLog",
		"APIError", "Limiter", "Limiter.Low", "PostArgs", "Posted", "PageArgs", "MessagesPage", "SearchArgs"},
	"internal/slack/auth": {"Token", "UserScopes", "Keychain", "SecurityKeychain", "Store", "Store.Load", "Store.Save",
		"Store.Delete", "Store.Token", "Store.AppToken", "Store.SaveAppToken", "Store.DeleteAppToken", "Login", "Login.Run"},
	"internal/slack/send": {"Request", "Resolved", "Sent", "Service", "Service.Resolve", "Service.Allowed", "Service.Send",
		"ErrNotLoggedIn", "ErrAmbiguous", "ErrCannotPost", "ErrNotAllowed", "Mrkdwn"},
	"internal/transport/mcp": {"Slack", "SlackReader", "SlackDrafts", "DraftArgs", "DraftResult",
		"Server.Slack", "Server.SlackRead", "Server.SlackDraft"},
	"internal/slack/socket": {"Envelope", "Envelope.EventID", "StateKind", "State", "Socket", "Conn", "Conn.Run",
		"ErrInvalidAuth", "RedactURL"},
	"internal/slack/socket/sockettest": {"New", "Server", "Server.URL", "Server.Open", "Server.Send", "Server.Hello",
		"Server.Disconnect", "Server.Drop", "Server.Acks", "Server.Opens"},
	"internal/domain/slack": {"Provider", "Kind", "Via", "ViaSocket", "ViaCatchUp", "ViaProbe", "ViaSearch", "ViaFollow",
		"File", "Reaction", "Message", "Delta", "Delta.EventID", "Codec", "FromEvent", "MessageFrom", "HiddenSubtypes",
		"ThreadObject", "DMObject", "ParseObject", "ThreadUnit", "ObjectOf", "ConversationKind", "Conversation", "User",
		"Facts", "Facts.Units", "Thread", "DMWindow", "ChangeKind", "Change", "ErrUnknownUnit", "FactsReader", "ObjectRef",
		"WatchReason", "Watcher", "Scheduler", "Segments", "Span", "SegmentWriter"},
	"internal/adapter/source/slack": {"Metrics", "Metrics.Received", "Metrics.Admitted", "Metrics.Socket", "Metrics.CatchUp",
		"Metrics.RateLimited", "Metrics.Backfill", "Metrics.Warn"},
	"internal/core/port": {"Heat", "HeatActive", "HeatWarm", "HeatCold", "HeatReader", "TopicLinks", "TopicItems",
		"TopicCards", "TopicEntries", "TopicDrafts", "Linker", "LinkStore", "Effects", "Validator"},
	"internal/adapter/source/slack/extract": {"Viewer", "Input", "Result", "ItemOut", "SegmentOut", "LinkOut", "LinkOut.Kind",
		"ParseTarget", "Prompt", "Schema", "Extract", "Ops", "Render", "UnitState", "Hint", "Reader", "Job", "Job.Run", "Job.Warnings"},
	"internal/adapter/store/sqlite": {"SchemaVersion", "ExtractionState.ClosureMarked"},
	"internal/domain/link":          {"Kind", "Kind.Valid", "Origin", "Facts", "Link", "EventKind", "Event"},
	"internal/core/ingest":          {"Report.Links", "Report.LinksDropped"},
	"internal/core/links":           {"EventReader", "Resolver", "Resolver.Run"},
	"internal/source": {"Deps.Units", "Deps.Links", "Plugin", "Module.Items", "Module.Card", "Module.Identity",
		"Module.UnitEvents", "Module.UI", "Module.WebAssets", "Module.CLI", "Module.Run", "Module.Health", "Module.Jobs",
		"Module.Publisher", "Module.Proposals", "Module.Payload", "Module.PlacementDefaults", "Module.PlacementHints",
		"Job", "CLI", "Module.OnPosted", "Module.Checkouts", "Module.Briefs", "Module.Links", "Module.Capabilities", "Checkouts", "Checkout", "LinkResolver"},
	"internal/domain/unit": {"Anchor", "Anchor.Valid", "Entry", "Equaler", "KeysEqual"},
	"internal/domain/items": {"Kind", "State", "Resolution", "Basis", "EvidenceKind", "Confidence", "Provenance", "Initial",
		"Person", "Person.Is", "Identity", "Option", "Evidence", "Item", "OpKind", "Op", "Op.ValidateShape", "Fields"},
	"internal/core/items": {"Applier", "Reconciler", "DisabledError", "Ledger", "Ledger.Apply", "Ledger.Reconcile",
		"Ledger.Items", "Ledger.Query", "Ledger.Proposals", "Ledger.Decide", "Query", "Result", "Applied", "Refused",
		"ProposalState", "Proposal", "Store", "Commit", "LogEntry"},
	"internal/core/instructions": {"OpenItem"},
	"internal/api": {"ItemsResult", "Plugin", "Plugins", "SourceHealth",
		"Detail.Card", "Detail.Items", "ActionSpec.Scope", "ActionSpec.Verb", "ActionSpec.Plugin",
		"PluginUI", "Facet", "SavedView", "Notification", "KeyBinding", "WebView", "MenubarSection",
		"Row.Payload", "Row.Summary", "Row.Facets", "Row.Items", "Summary", "RowItem", "Catalog", "CatalogResponse",
		"ViewSpec", "FacetSpec", "Bucket", "SituationSpec", "Message", "GTDCatalog", "EntryView", "EntryView.Message",
		"EntryView.Draft", "EntriesPage", "CaptureRequest", "EntryRequest", "EntryResult", "Counters", "Change.Counters",
		"RowsPage.Counters", "Category", "NotificationID", "ParseNotificationID", "Digest", "Draft", "DraftAction", "Draft.Purpose", "Draft.Delivery", "Draft.Effects", "Plugin.Capabilities", "Brief.Extra", "BriefSummary.Extra", "Turn.Role"},
	"internal/config": {"Plugin", "Config.Plugins", "Config.PluginEnabled", "Turns", "Config.Turns", "WorkflowConfig.DraftApproval", "DraftApprovalConfig", "DraftApprovalConfig.Policy", "ApprovalRuleConfig"},
	"internal/daemon": {"Deps.Plugins", "Deps.PluginEnabled", "writePluginDisabled", "Server.refusePlugin"},
	"internal/domain/card": {"Tone", "Signal", "Human", "Mute", "Card", "Card.StartedByMe", "Card.ClosurePosted", "Card.Cares",
		"Card.LastHuman", "Card.Outcome", "Snapshot", "Store"},
	"internal/client/tui":      {"PluginView", "RegisterView"},
	"internal/readmodel":       {"Presenter"},
	"internal/readmodel/prrow": {"Payload", "LastHuman", "Mute", "PayloadOf", "NeedsMe", "InScope"},
	"internal/turn": {"Payload", "Wording", "PayloadReader", "ItemsReader", "Context.Unit", "Context.Payload",
		"Context.Wording", "Context.Items", "Request.Payload", "Builder.Payloads", "Builder.Items"},
	"internal/domain/intent":    {"Def.Provider", "Def.DraftApproval", "Def.Modes", "Def.Role", "Def.Builds", "Def.Prompt", "Def.Schema", "Def.WorkspaceRepo", "Def.DraftEffects", "Def.RoundKind", "Def.Cap", "Def.ReasonFact", "Def.RefuseWhen", "Def.Reason", "Def.Providers", "WorkspaceRepo", "Brief.Need", "Registry.ByRole", "Registry.Builder"},
	"internal/domain/placement": {"Placement", "Rule", "Map", "Overrides", "Hints", "Defaults"},
	"internal/core/placement":   {"Service", "Service.Place", "ErrUnplaced"},
	"internal/core/turns": {"Service.SessionFor", "Live", "ReadOnlyTools", "ErrToolNotAllowed",
		"ErrNoRestricted", "HeadlessStart", "HeadlessEnd", "Policy.Dirs", "Dir"},
	"internal/domain/turn":    {"Session.Unit", "Mode", "Turn.Mode", "Turn.Workflow", "Turn.Role"},
	"internal/domain/message": {"Kind", "State", "Origin", "Message", "Store"},
	"internal/core/messages": {"Service", "Service.Reconcile", "Service.ReconcileProposals", "Service.Post",
		"Service.Propose", "Service.Answer", "Service.Dismiss", "PostRequest", "ProposeRequest", "Answer"},
	"internal/domain/gtd": {"State", "Kind", "ID", "ItemRef", "Focus", "Snooze", "WaitingOn", "Entry", "Me", "Mute",
		"UnitFacts", "ChangeOp", "Change", "Reconcile", "Activity", "AutoFocus", "Wake", "Verb", "View", "Query", "Counters", "Codec", "UnitOf"},
	"internal/core/gtd": {"Store", "Since", "Page", "Items", "Facts", "ViewSource", "VerbHandler", "Request", "Result",
		"Service", "Service.Register", "Service.RegisterView", "Service.Do", "Service.Capture", "Service.Open",
		"Service.Close", "Service.Sync", "Service.Counts", "OpenRequest"},
	"internal/keys": {"Surface", "Context", "Binding", "Core", "Canonical", "Reserved", "ErrReserved", "Check"},
	"internal/domain/draft": {"ID", "Kind", "State", "OriginKind", "Origin", "Evidence", "WouldPolicy", "Outcome", "Would",
		"Draft", "Draft.Can", "ErrTransition", "ApprovalMode", "Approval", "Approval.Validate", "Approval.Releases", "Policy", "Policy.Validate", "Policy.Decide", "Policy.ReadsSubject", "Subject", "Rule", "Condition", "Decision", "Fixed", "Draft.RuleFacts", "Origin.Proposer", "SettledAt", "WithdrawAssigned", "WithdrawMoot", "Draft.EffectsAppliedAt"},
	"internal/core/outbox": {"Proposal", "Filter", "Store", "Effects", "Evidence", "Service", "Service.Propose",
		"Service.Edit", "Service.Approve", "Service.Discard", "Service.Retry", "Service.List", "Service.RecordSent",
		"Service.Send", "Service.Recover", "Service.Tick", "Service.NextDeadline", "ErrNotFound", "ErrConflict",
		"ErrDisabled", "ErrDuplicate", "ErrNeedsConfirm", "Policies", "Proposal.Purpose", "Proposal.Delivery", "Proposal.Effects", "Proposal.RecordedFor", "Proposal.Policy", "Proposal.Subject", "Service.Broken", "Facts", "Service.Facts", "Filter.Workflow", "Effect", "Applier", "Service.Applier"},
	"internal/domain/workflow": {"Workflow.DraftApproval", "Workflow.Withdraw"},
	"internal/core/headless":   {"Request", "Result", "Builder", "Placer", "Service", "ErrBusy"},
	"internal/domain/dossier":  {"Owner", "Status", "Dossier", "Policy", "Dir"},
	"internal/core/dossiers": {"Files", "Change", "Service", "Service.Open", "Service.Get", "Service.Resolved",
		"Service.Reopened", "Service.Sweep", "Service.Report", "Report", "Usage"},
	"internal/domain/jsonschema":   {"Keywords", "Schema", "Options", "Compile", "Schema.Validate", "Schema.ValidateJSON", "Schema.Properties", "Property"},
	"internal/core/automation":     {"Withdrawals", "ProposedDraft", "Engine.Withdrawals"},
	"internal/intent/builtin":      {"Effect", "Effects", "EffectNames", "StepDraftPropose", "Roles", "Builds", "RoundKinds", "Caps", "TurnReasons", "TurnModes", "DraftDeliveries", "DraftItems", "DraftPer", "DraftTargets", "HoldForever", "RoundProviders", "ExtraFacts", "WithdrawEnv", "ApprovalEnv", "ApprovalRule", "ApprovalSlot", "DraftDelivery", "DraftPurpose", "DraftEffects", "AutoOnly", "NeverReleasesComment", "UntrustedError", "Fact.Untrusted", "UnitSlots", "DraftSlot", "BriefSubjects", "Fact.Dim", "Fact.Subjects"},
	"internal/brief":               {"BriefSource", "UnitInput", "Composed", "Compose", "Composed.Validate", "Composed.FieldVersions", "Brief.Extra", "Brief.ExtraVersions", "Summary.Extra"},
	"internal/profile":             {"Resolve", "Files", "Resolved", "PartialsDir", "PromptsDir"},
	"internal/adapter/facts/entry": {"Provider"},
	"internal/adapter/facts/slack": {"Provider"},
}

// checker type-checks a module package from its source, importing its
// dependencies from the export data go list builds, so unexported names
// resolve too.
type checker struct {
	imp   types.Importer
	files map[string][]string
}

func newChecker(t *testing.T, pkgs []string) checker {
	t.Helper()
	args := append([]string{"list", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}\t{{.Dir}}\t{{join .GoFiles \",\"}}"}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -export: %v", err)
	}
	exports := map[string]string{}
	c := checker{files: map[string][]string{}}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 4 {
			continue
		}
		exports[f[0]] = f[1]
		for _, name := range strings.Split(f[3], ",") {
			if name != "" {
				c.files[f[0]] = append(c.files[f[0]], filepath.Join(f[2], name))
			}
		}
	}
	c.imp = importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, os.ErrNotExist
		}
		return os.Open(file)
	})
	return c
}

func (c checker) check(path string) (*types.Package, error) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range c.files[path] {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return (&types.Config{Importer: c.imp}).Check(path, fset, files, nil)
}

// undeclared lists the names of names that do not resolve in pkg.
func undeclared(pkg *types.Package, names []string) []string {
	var out []string
	for _, name := range names {
		typ, member, nested := strings.Cut(name, ".")
		obj := pkg.Scope().Lookup(typ)
		if obj == nil {
			out = append(out, name)
			continue
		}
		if !nested {
			continue
		}
		if m, _, _ := types.LookupFieldOrMethod(obj.Type(), true, pkg, member); m == nil {
			out = append(out, name)
		}
	}
	return out
}

// TestContractSkeletonDeclared checks that the shared names other packages
// build on still exist.
func TestContractSkeletonDeclared(t *testing.T) {
	t.Parallel()
	var pkgs []string
	for p := range skeleton {
		pkgs = append(pkgs, module+p)
	}
	c := newChecker(t, pkgs)
	for p, names := range skeleton {
		pkg, err := c.check(module + p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		for _, name := range undeclared(pkg, names) {
			t.Errorf("%s.%s is not declared", p, name)
		}
	}
}

func TestContractSkeletonFlagsAMissingName(t *testing.T) {
	t.Parallel()
	pkg, err := newChecker(t, []string{module + "internal/daemon"}).check(module + "internal/daemon")
	if err != nil {
		t.Fatal(err)
	}
	got := undeclared(pkg, []string{"Deps", "Deps.Plugins", "writePluginDisabled", "Server.refusePlugin", "Absent", "Deps.Absent", "Server.absent"})
	flagged(t, "the missing names", got, 3, "bsent")
	if strings.Join(got, ",") != "Absent,Deps.Absent,Server.absent" {
		t.Errorf("%v", got)
	}
}
