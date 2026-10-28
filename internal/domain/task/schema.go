package task

// Category is the closed set a profile's state belongs to; Go reads a
// task's category, never its state's name.
type Category string

const (
	CategoryInbox    Category = "inbox"
	CategoryReady    Category = "ready"
	CategoryActive   Category = "active"
	CategoryWaiting  Category = "waiting"
	CategoryDeferred Category = "deferred"
	CategoryDone     Category = "done"
	CategoryDropped  Category = "dropped"
)

// Categories are every category, in the order a task moves through them.
var Categories = []Category{CategoryInbox, CategoryReady, CategoryActive, CategoryWaiting, CategoryDeferred, CategoryDone, CategoryDropped}

// PropType is the type a profile declares for a task property.
type PropType string

const (
	PropString   PropType = "string"
	PropStrings  PropType = "string[]"
	PropInt      PropType = "int"
	PropEnum     PropType = "enum"
	PropDate     PropType = "date"
	PropDuration PropType = "duration"
	PropActor    PropType = "actor"
	PropActors   PropType = "actor[]"
)

// PropTypes are every property type.
var PropTypes = []PropType{PropString, PropStrings, PropInt, PropEnum, PropDate, PropDuration, PropActor, PropActors}

// Writer is who wrote a property.
type Writer string

const (
	WriterPerson   Writer = "person"
	WriterWorkflow Writer = "workflow"
	WriterAgent    Writer = "agent"
	WriterPlugin   Writer = "plugin"
)

// Writers are every writer.
var Writers = []Writer{WriterPerson, WriterWorkflow, WriterAgent, WriterPlugin}

// Trusted says whether an approval rule may read what w wrote: an agent's
// and a plugin's writes come from text no person vouched for.
func (w Writer) Trusted() bool { return w == WriterPerson || w == WriterWorkflow }

// Actor is who ran a transition or resolved a gate; the same set as Writer.
type Actor string

const (
	ActorPerson   Actor = "person"
	ActorWorkflow Actor = "workflow"
	ActorAgent    Actor = "agent"
	ActorPlugin   Actor = "plugin"
)

// Actors are every actor.
var Actors = []Actor{ActorPerson, ActorWorkflow, ActorAgent, ActorPlugin}
