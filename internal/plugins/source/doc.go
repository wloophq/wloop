// Package source adapts the source kind of plugins to the daemon: the
// host's side of the broker a source plugin calls, one file per broker
// interface; the host adapters of each capability (publisher, briefs,
// payload, placement, checkouts, tools); the manifest codecs; and the kind
// adapter the plugin host starts each source plugin through. It converts the
// protocol's units, task ids and drafts to the domain's.
//
// No broker method takes the loop's lock: a broker call runs on the plugin
// connection's goroutine while the host may hold that lock around the call
// that led the plugin to make it.
package source
