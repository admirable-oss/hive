// Package event is the daemon's event bus. Domain packages publish what
// happened (a process started, an environment was removed); clients
// subscribe over the wire (events.subscribe) instead of polling.
//
// Publishing never blocks. Each subscriber has a bounded queue; one that
// falls behind loses events and is told so with an events_lost event, after
// which it should re-read the state it cares about.
package event
