// Package htmlview renders a fork forest as a self-contained HTML page.
//
// The output has no external references — no CDN, no fonts, no server. Data is
// embedded as JSON and the page is a single file you can open with file://,
// copy to another machine, or attach to a ticket.
package htmlview

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/khambampati-subhash/claude-ckpt/internal/forest"
)

//go:embed page.html
var page string

// excerptLimit caps how much of each message is embedded. Transcripts run to
// megabytes, most of it tool output nobody will scroll through; a generous
// excerpt keeps the page openable while staying useful.
const excerptLimit = 4000

// Message is one checkpoint as rendered in the page.
type Message struct {
	UUID     string `json:"uuid"`
	Short    string `json:"short"`
	Role     string `json:"role"`
	Summary  string `json:"summary"`
	Text     string `json:"text"`
	Branches bool   `json:"branches"`
}

// Session is one node of the fork tree.
type Session struct {
	ID        string    `json:"id"`
	Short     string    `json:"short"`
	Title     string    `json:"title"`
	Messages  int       `json:"messages"`
	Parent    string    `json:"parent"`
	ForkPoint string    `json:"forkPoint"`
	ForkFrom  string    `json:"forkFrom"`
	Own       int       `json:"own"`
	Inherited int       `json:"inherited"`
	Inferred  bool      `json:"inferred"`
	Children  []Session `json:"children"`
	Timeline  []Message `json:"timeline"`
}

type data struct {
	Project   string    `json:"project"`
	Generated string    `json:"generated"`
	Roots     []Session `json:"roots"`
	Sessions  int       `json:"sessions"`
	Forks     int       `json:"forks"`
}

// Render writes the page for a forest to w.
func Render(f *forest.Forest, project string, w io.Writer) error {
	d := data{
		Project:   project,
		Generated: time.Now().Format("2006-01-02 15:04"),
		Sessions:  len(f.Nodes),
	}
	for _, n := range f.Nodes {
		if n.Parent != nil {
			d.Forks++
		}
	}
	for _, root := range f.Roots {
		d.Roots = append(d.Roots, convert(root))
	}

	encoded, err := json.Marshal(d)
	if err != nil {
		return err
	}
	// json.Marshal escapes <, > and & by default, so the payload cannot break
	// out of the surrounding <script> element.
	_, err = io.WriteString(w, strings.Replace(page, "/*DATA*/null", string(encoded), 1))
	return err
}

func convert(n *forest.Node) Session {
	// Both slices start non-nil: a nil slice marshals to JSON null, and a
	// session with nothing displayable would then crash the page.
	s := Session{
		Timeline:  []Message{},
		Children:  []Session{},
		ID:        n.Session.ID,
		Short:     shorten(n.Session.ID),
		Title:     n.Title,
		Messages:  n.Messages,
		ForkPoint: shorten(n.ForkPoint),
		Own:       n.Own,
		Inherited: n.Inherited,
		Inferred:  n.Inferred,
	}
	if n.Parent != nil {
		s.Parent = shorten(n.Parent.Session.ID)
		if rec, ok := n.Parent.Transcript.Get(n.ForkPoint); ok {
			s.ForkFrom = rec.Summary()
		}
	}

	for _, rec := range n.Transcript.MainLine() {
		if rec.Type() == "attachment" || rec.IsSidechain() {
			continue
		}
		summary := rec.Summary()
		if summary == "" {
			continue
		}
		role := rec.Role()
		if role == "" {
			role = rec.Type()
		}
		text := rec.Text()
		if len(text) > excerptLimit {
			text = text[:excerptLimit] + fmt.Sprintf("\n\n… truncated (%d more characters)", len(rec.Text())-excerptLimit)
		}
		s.Timeline = append(s.Timeline, Message{
			UUID:     rec.UUID(),
			Short:    shorten(rec.UUID()),
			Role:     role,
			Summary:  summary,
			Text:     text,
			Branches: n.Transcript.ChildCount(rec.UUID()) > 1,
		})
	}

	for _, c := range n.Children {
		s.Children = append(s.Children, convert(c))
	}
	return s
}

func shorten(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
