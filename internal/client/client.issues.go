package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/issue"
)

// Issues is the typed issue API (issue.*) over any Client.
type Issues struct {
	c Client
}

// NewIssues wraps c.
func NewIssues(c Client) Issues { return Issues{c: c} }

// Get reads an issue by its ref (tracker:key).
func (i Issues) Get(ctx context.Context, ref string) (issue.Issue, error) {
	var r issue.Issue
	err := i.c.Call(ctx, "issue.get", issue.RefParams{Ref: ref}, &r)
	return r, err
}

// Search finds issues in one tracker, or in every one ("").
func (i Issues) Search(ctx context.Context, tracker, query string, limit int) ([]issue.Issue, error) {
	var r []issue.Issue
	err := i.c.Call(ctx, "issue.search", issue.SearchParams{Tracker: tracker, Query: query, Limit: limit}, &r)
	return r, err
}

// Comment adds a comment to an issue.
func (i Issues) Comment(ctx context.Context, ref, body string) error {
	return i.c.Call(ctx, "issue.comment", issue.CommentParams{Ref: ref, Body: body}, nil)
}

// Close closes an issue, with a comment when one is given.
func (i Issues) Close(ctx context.Context, ref, comment string) (issue.Issue, error) {
	var r issue.Issue
	err := i.c.Call(ctx, "issue.close", issue.CloseParams{Ref: ref, Comment: comment}, &r)
	return r, err
}
