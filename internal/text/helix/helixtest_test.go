// Copyright (C) 2017-2026 The Rune Authors
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or (at
// your option) any later version.
//
// This program is distributed in the hope that it will be useful, but
// WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
// General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package helix

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/clipboard"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/text"
	"unstable.build/rune/internal/text/registerset"
)

// newHelix builds a real Helix handler over content with an in-memory
// clipboard, positions the caret, and returns the handler plus the
// backing buffer and clipboard for assertions.
func newHelix(
	t *testing.T, content string, at term.Coordinates, opts ...Option,
) (*Helix, *cell.Buffer, clipboard.Register) {
	t.Helper()
	return newHelixURI(t, "test:///", content, at, opts...)
}

func newHelixURI(
	t *testing.T, uri, content string, at term.Coordinates, opts ...Option,
) (*Helix, *cell.Buffer, clipboard.Register) {
	t.Helper()
	resource, err := workspaceapi.ParseURI(uri)
	require.NoError(t, err)
	buf := cell.NewBuffer()
	buf.ReadFrom(strings.NewReader(content))
	// clipboard.NewInMemory has a single slot; registerset adds the
	// per-register addressing the " prefix needs.
	clip := registerset.New(clipboard.NewInMemory())
	hx := NewWithIndent(buf, resource, text.IndentRuneSpace, 2,
		append([]Option{WithClipboard(clip), WithTabspaces(2)}, opts...)...)
	hx.Resize(80, 20)
	if at != (term.Coordinates{}) {
		hx.SetCursorAtScroll(at)
	}
	return hx, buf, clip
}

func key(ch rune) term.Event {
	return term.Event{Type: term.EventKey, Ch: ch}
}

func modKey(mod term.Modifier, ch rune) term.Event {
	return term.Event{Type: term.EventKey, Mod: mod, Ch: ch}
}

func namedKey(k term.Key) term.Event {
	return term.Event{Type: term.EventKey, Key: k}
}

func modNamedKey(mod term.Modifier, k term.Key) term.Event {
	return term.Event{Type: term.EventKey, Mod: mod, Key: k}
}

// keys turns a literal chord string into plain key events. Modified and
// named keys are built with modKey/namedKey and appended explicitly.
func keys(s string) []term.Event {
	evs := make([]term.Event, 0, len(s))
	for _, ch := range s {
		evs = append(evs, key(ch))
	}
	return evs
}

func send(t *testing.T, hx *Helix, evs ...term.Event) {
	t.Helper()
	for _, ev := range evs {
		hx.Handle(ev)
	}
}

// sel returns the current selection text. Selection reports ok=false for
// a caret on an empty line, where the one-cell range covers no text.
func sel(t *testing.T, hx *Helix) string {
	t.Helper()
	s, _ := hx.Selection()
	return s
}

// seedClipboard puts text in the default register with the given
// selection metadata so paste tests are deterministic.
func seedClipboard(
	t *testing.T, clip clipboard.Register, str string, mode text.SelectMode,
) {
	t.Helper()
	require.NoError(t, clip.Copy(clipboard.DefaultRegisterID,
		clipboard.Data{Text: str, Metadata: mode}))
}

// testIndentView supplies the syntax indent targets ReindentSelection
// needs, which a plain cell.View does not provide.
type testIndentView struct {
	cell.View
	indents map[int]int
}

func (v testIndentView) IndentationAt(line int) (int, bool) {
	target, ok := v.indents[line]
	return target, ok
}

// testCommentView reports which lines are already commented, which is
// the syntax service Cursor.ToggleLineComment consults before it will
// uncomment anything.
type testCommentView struct {
	cell.View
	line []string
}

func (v testCommentView) CommentCoverage(rng term.Range) ([]term.Range, bool) {
	start, end := term.CoordinatesSort(rng.Start, rng.End)
	var ranges []term.Range
	for y := start.Y; y <= end.Y && y < v.Rows(); y++ {
		line := term.CellsToString([][]term.Cell{v.RawCells()[y]})
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		for _, prefix := range v.line {
			if !strings.HasPrefix(trimmed, prefix) {
				continue
			}
			ranges = append(ranges, term.Range{
				Start: term.Coordinates{Y: y, X: indent},
				End:   term.Coordinates{Y: y, X: len(line)},
			})
			break
		}
	}
	if len(ranges) == 0 {
		return nil, false
	}
	return ranges, true
}

// testSelectionView supplies the syntax-tree expansion A-o / A-i walk.
type testSelectionView struct {
	cell.View
	expand map[term.Range]term.Range
	shrink map[term.Range]term.Range
}

func (v testSelectionView) SelectionExpand(rng term.Range) (term.Range, bool) {
	next, ok := v.expand[rng]
	return next, ok
}

func (v testSelectionView) SelectionShrink(
	rng term.Range, caret term.Coordinates,
) (term.Range, bool) {
	next, ok := v.shrink[rng]
	return next, ok
}
