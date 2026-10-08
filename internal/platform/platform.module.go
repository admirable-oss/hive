// Package platform hides operating-system specifics behind plain functions so
// domain packages stay portable. Each capability has one file per OS; a
// fallback file returns ErrUnsupported where Hive has no implementation yet.
package platform
