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
	"internal/transport/mcp": {"PluginTool", "Server.Plugins"},
	"internal/core/port": {"Heat", "HeatActive", "HeatWarm", "HeatCold", "HeatReader", "TopicLinks", "TopicItems",
		"TopicCards", "TopicTasks", "TopicDrafts", "Linker", "LinkStore", "Effects", "Validator",
		"Topic", "TopicPending", "TopicGates", "TopicWakes", "TopicTimeline", "TopicRuns"},
	"internal/adapter/store/sqlite": {"SchemaVersion", "DB.PutUnitBrief", "PropStore", "PropStore.SetProp",
		"PropStore.PropsOf", "DB.Props", "v7Props", "v7Gates", "v7Briefs", "v7Kinds", "v7Wakes", "v7PRTasks", "v7Dossiers",
		"v7PluginStores", "v7Snapshots"},
	"internal/domain/link": {"Kind", "Kind.Valid", "Origin", "Facts", "Link", "EventKind", "Event"},
	"internal/core/ingest": {"Report.Links", "Report.LinksDropped"},
	"internal/core/links":  {"EventReader", "Resolver", "Resolver.Run"},
	"internal/source": {"Deps.Units", "Deps.Links", "Plugin", "Module.Items", "Module.Card", "Module.Identity",
		"Module.UnitEvents", "Module.UI", "Module.WebAssets", "Module.CLI", "Module.Run", "Module.Health", "Module.Jobs",
		"Module.Publisher", "Module.Proposals", "Module.Payload", "Module.PlacementDefaults", "Module.PlacementHints",
		"Job", "CLI", "Module.OnPosted", "Module.Checkouts", "Module.Briefs", "Module.Links", "Module.Capabilities", "Checkouts", "Checkout", "LinkResolver"},
	"internal/domain/duration": {"Parse", "Format"},
	"internal/intent":          {"BuiltinAction", "Loaded.Actions", "ActionPrograms"},
	"internal/domain/unit":     {"Anchor", "Anchor.Valid", "Task", "Equaler", "KeysEqual", "Resolver", "Locator"},
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
		"ViewSpec", "FacetSpec", "Bucket", "SituationSpec", "Message", "ViewsCatalog", "TaskView",
		"TasksPage", "TaskCreate", "TaskCreate.Title", "TaskCreate.Brief", "TaskCreate.Props",
		"TaskCreate.Placement", "TaskCreate.Unit", "TaskView.Category", "TaskView.Props", "TaskView.OwedBy", "TaskView.Asks",
		"TaskView.Gates", "TaskView.Runs", "TaskView.Brief", "TaskView.Parent", "TaskView.Children", "TaskView.ReviewDue",
		"AskView", "GateView", "IntentView", "RunView", "RecordView", "TimelineEvent", "ChatRequest", "WakeView",
		"TaskView.Wake", "TaskViewSpec.Over", "TaskViewSpec.Group", "TaskViewSpec.Key", "TaskViewSpec.Badge",
		"TaskViewSpec.HideDisabled", "ViewsCatalog.Review", "ViewsCatalog.States", "ViewsCatalog.Props", "StateSpec",
		"StateSpec.Category", "PropSpec", "PropSpec.Writers", "VerbSpec.From", "VerbSpec.To", "IntentView.Answer",
		"TasksPage.Gates", "GateView.Task", "GateView.Unit", "GateView.Delivery", "GateView.Default", "TimelineEvent.Seq",
		"TimelinePath", "RunsPath", "ChatPath", "AttachPath", "GatesPath", "GatePath", "RunOpen", "RunEnter", "RunTranscript",
		"TaskRequest", "TaskResult", "Counters", "Change.Counters",
		"RowsPage.Counters", "Category", "NotificationID", "ParseNotificationID", "Digest", "Draft", "Draft.Gate", "Draft.Intents", "TaskView.DraftOf", "Draft.Purpose", "Draft.Delivery", "Draft.Effects", "Plugin.Capabilities", "Brief.Extra", "BriefSummary.Extra", "Turn.Role", "ActionSpec.Summary", "ActionState.Note", "ActionState.Warning", "PlanItem.Observe", "ObserveMode", "RunnerState", "Turn.Runner"},
	"internal/config": {"Plugin", "Config.Plugins", "Config.PluginEnabled", "Turns", "Config.Turns", "WorkflowConfig.GateApproval", "ApprovalConfig", "ApprovalRuleConfig"},
	"internal/daemon": {"Deps.Plugins", "Deps.PluginEnabled", "writePluginDisabled", "Server.refusePlugin"},
	"internal/domain/card": {"Tone", "Signal", "Human", "Mute", "Card", "Card.StartedByMe", "Card.ClosurePosted", "Card.Cares",
		"Card.LastHuman", "Card.Outcome", "Snapshot", "Store"},
	"internal/client/tui":      {"PluginView", "RegisterView"},
	"internal/readmodel":       {"Presenter"},
	"internal/readmodel/prrow": {"Payload", "LastHuman", "Mute", "PayloadOf", "NeedsMe", "InScope"},
	"internal/turn": {"Payload", "Wording", "PayloadReader", "ItemsReader", "Context.Unit", "Context.Payload",
		"Context.Wording", "Context.Items", "Request.Payload", "Builder.Payloads", "Builder.Items"},
	"internal/domain/intent":    {"Def.Provider", "Def.DraftApproval", "Def.Runners", "Def.Role", "Def.Builds", "Def.Prompt", "Def.Schema", "Def.WorkspaceRepo", "Def.AllowedEffects", "Def.RoundKind", "Def.Why", "Def.Refuse", "Def.Providers", "WorkspaceRepo", "Brief.Need", "Registry.ByRole", "Registry.Builder", "Def.Effects", "Action.When", "Action.Note", "Action.Warning"},
	"internal/domain/placement": {"Placement", "Rule", "Map", "Overrides", "Hints", "Defaults"},
	"internal/core/placement":   {"Service", "Service.Place", "ErrUnplaced"},
	"internal/core/turns": {"Run", "TranscriptLine", "Transcripts", "Transcripts.Page", "Service.SessionFor", "Live", "ReadOnlyTools", "ErrToolNotAllowed",
		"ErrNoRestricted", "HeadlessStart", "HeadlessEnd", "Policy.Dirs", "Dir"},
	"internal/domain/turn":    {"Session.Unit", "Runner", "Turn.Runner", "Turn.Workflow", "Turn.Role"},
	"internal/domain/message": {"Kind", "Origin", "Message", "Store"},
	"internal/core/messages": {"Service", "Service.Reconcile", "Service.ReconcileProposals", "Service.Post",
		"Service.Propose", "PostRequest", "ProposeRequest", "Answer"},
	"internal/domain/task": {"Category", "Categories", "PropType", "PropTypes", "Writer", "Writers", "Writer.Trusted",
		"Actor", "Actors", "Value", "Value.IsZero", "Props", "Props.Get", "State", "Kind", "ID", "ItemRef", "Snooze", "WaitingOn", "Task", "Me", "Mute",
		"UnitFacts", "ChangeOp", "Change", "Reconcile", "Activity", "AutoFocus", "Wake", "Verb", "View", "Query", "Counters", "Codec", "UnitOf"},
	"internal/core/tasks": {"PropStore", "PropStore.SetProp", "PropStore.PropsOf", "ViewEval", "Authority", "OwedBy", "Store", "Since", "Page", "Items", "Facts", "ViewSource", "VerbHandler", "Request", "Result",
		"Service", "Service.Register", "Service.RegisterView", "Service.Do", "Service.Capture", "Service.Open",
		"Service.Close", "Service.Sync", "Service.Counts", "OpenRequest"},
	"internal/keys": {"Surface", "Context", "Binding", "Core", "Canonical", "Reserved", "ErrReserved", "Check"},
	"internal/domain/draft": {"ID", "Kind", "State", "OriginKind", "Origin", "Evidence",
		"Draft", "Draft.Can", "ErrTransition", "ApprovalMode", "Approval", "Approval.Validate", "Approval.Releases", "Policy", "Policy.Validate", "Policy.Decide", "Policy.ReadsSubject", "Subject", "Rule", "Condition", "Decision", "Fixed", "Draft.RuleFacts", "Origin.Proposer", "SettledAt", "WithdrawAssigned", "WithdrawMoot", "Draft.EffectsAppliedAt", "DryRun", "Held", "Queued", "Dropped"},
	"internal/core/outbox": {"Proposal", "Filter", "Store", "Effects", "Evidence", "Service", "Service.Propose",
		"Service.Edit", "Service.Prepare", "Service.Discard", "Service.Withdraw", "Service.Retry", "Service.List", "Service.RecordSent",
		"Service.Send", "Service.Recover", "Service.Publish", "Service.Outbox", "Service.GateOf", "Service.DeliveryOf", "Gates", "Service.Gates", "Pending",
		"ErrNotFound", "ErrConflict",
		"ErrDisabled", "ErrDuplicate", "ErrNeedsConfirm", "Policies", "Proposal.Purpose", "Proposal.Delivery", "Proposal.Effects", "Proposal.RepeatAfter", "Proposal.Policy", "Proposal.Subject", "Service.Broken", "Facts", "Service.Facts", "Filter.Workflow", "Effect", "Applier", "Service.Applier"},
	"internal/domain/workflow": {"Workflow.DraftApproval", "Workflow.Withdraw", "Workflow.Observe", "Item.Observe"},
	"internal/core/headless":   {"Request", "Result", "Builder", "Placer", "Service", "ErrBusy"},
	"internal/domain/dossier":  {"Owner", "Status", "Dossier", "Policy", "Dir"},
	"internal/core/dossiers": {"Files", "Change", "Service", "Service.Open", "Service.Get", "Service.Resolved",
		"Service.Reopened", "Service.Sweep", "Service.Report", "Report", "Usage"},
	"internal/domain/jsonschema": {"Keywords", "Schema", "Options", "Compile", "Schema.Validate", "Schema.ValidateJSON", "Schema.Properties", "Property"},
	"internal/core/automation":   {"Withdrawals", "ProposedDraft", "Engine.Withdrawals"},
	"internal/intent/builtin": {"Effect", "Effects", "EffectNames", "StepDraftPropose", "Roles", "Builds", "TurnRunners", "DraftDeliveries", "DraftItems", "DraftPer", "DraftTargets", "RepeatForever", "RoundProviders", "ExtraFacts", "WithdrawEnv", "ApprovalEnv", "ApprovalRule", "ApprovalSlot", "SurfaceRecords", "PropPrefix", "LinkSignalPrefix", "GateSlot",
		"StepGateOpen", "StepTaskSet", "VerbHandlers", "VerbHandler", "DraftDelivery", "DraftPurpose", "DraftEffects", "AutoOnly", "NeverNamesADelivery", "UntrustedError", "Fact.Untrusted", "UnitSlots", "DraftSlot", "BriefSubjects", "Fact.Dim", "Fact.Subjects", "IsUntrusted", "ActionFacts", "TurnEffectNames", "Grammar", "Key", "Value", "Kind", "Surface", "Problem", "Check"},
	"internal/brief":              {"BriefSource", "UnitInput", "Composed", "Compose", "Composed.Validate", "Composed.FieldVersions", "Brief.Extra", "Brief.ExtraVersions", "Summary.Extra"},
	"internal/profile":            {"Resolve", "Files", "Resolved", "PartialsDir", "PromptsDir"},
	"internal/adapter/facts/task": {"Provider"},
	"internal/domain/gate": {"Kind", "Kinds", "KindDraft", "KindPlan", "KindQuestion", "KindPost", "KindProposal", "KindTrust",
		"KindStep", "Status", "Open", "Approved", "Rejected", "Answered", "Dismissed", "Superseded", "Gate", "Gate.Task",
		"Gate.Unit", "Gate.Deadline", "Gate.Default", "Gate.Intents", "Gate.Intent"},
	"internal/domain/wake": {"Wake", "Wake.Task", "Wake.Until", "Wake.On", "Wake.Actors", "Wake.When", "Wake.Expires",
		"Wake.Return", "EventWoke"},
	"internal/core/gates": {"Store", "Store.Open", "Store.Resolve", "Store.Due", "Reader", "Reader.Get", "Reader.OfTask",
		"Reader.Where", "Resolution", "Resolution.Intent", "Query", "Query.Statuses", "Publisher", "Releaser", "Deliverer", "Decider", "Unblocker", "Sent", "Sent.RecordSent",
		"ErrNoGate", "ErrResolved", "Service.Supersede", "Service.RecordSent"},
	"internal/core/gates/gatestest":       {"Store", "NewStore", "Consequences", "StoreContract"},
	"internal/core/wakes":                 {"Store", "Store.Put", "Store.Drop", "Store.Of", "Store.Due", "Store.Watching", "Holds"},
	"internal/core/wakes/wakestest":       {"Store", "NewStore", "StoreContract"},
	"internal/core/timeline":              {"Reader", "Reader.Of", "Filter", "Filter.Before", "Event", "Event.Seq"},
	"internal/core/timeline/timelinetest": {"Reader", "NewReader"},
	"internal/core/tasks/taskstest":       {"Props", "NewProps", "PropContract"},
	"internal/core/turns/turnstest":       {"Transcripts"},
	"internal/adapter/agent/claude":       {"Transcripts"},
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
