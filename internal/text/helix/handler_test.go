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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/api/textapi"
	"github.com/unstablebuild/rune-go-sdk/api/workspaceapi"
	"github.com/unstablebuild/rune-go-sdk/clipboard"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/cell"
	"unstable.build/rune/internal/component"
	"unstable.build/rune/internal/text"
	"unstable.build/rune/internal/text/registerset"
)

// TestHandlerSatisfiesTextHandler pins the interface the IDE depends on.
func TestHandlerSatisfiesTextHandler(t *testing.T) {
	var _ text.Handler = (*Helix)(nil)
	hx, _, _ := newHelix(t, "abc", term.Coordinates{})
	assert.Equal(t, "test:///", hx.Resource().String())
	assert.NotNil(t, hx.CellView())
	assert.NotNil(t, hx.CellEditor())
}

// TestSelectionInvariantHolds is the single rule the whole grammar rests
// on: normal mode always owns at least the cell under the caret.
func TestSelectionInvariantHolds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		evs     []term.Event
	}{
		{name: "fresh handler", content: "abc"},
		{name: "after a motion", content: "foo bar", evs: keys("w")},
		{name: "after a delete", content: "foo bar", evs: keys("wd")},
		{name: "after a paste", content: "foo", evs: keys("yp")},
		{name: "after undo", content: "abc", evs: keys("du")},
		{name: "after leaving insert", content: "abc",
			evs: []term.Event{key('i'), key('X'), namedKey(term.KeyEsc)}},
		{name: "after esc in select mode", content: "abc",
			evs: []term.Event{key('v'), key('l'), namedKey(term.KeyEsc)}},
		{name: "after a failed motion at the buffer start", content: "abc",
			evs: keys("hhhh")},
		{name: "after a failed motion at the buffer end", content: "abc",
			evs: keys("llllll")},
		{name: "on an empty line", content: "a\n\nb", evs: keys("j")},
		{name: "on a wide glyph", content: "世界", evs: keys("l")},
		{name: "after select all", content: "a\nb", evs: keys("%")},
		{name: "after a text object", content: "foo bar", evs: keys("miw")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, _, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			_, ok := hx.cursor.SelectionMode()
			assert.True(t, ok || hx.buf.Rows() == 0,
				"normal mode must always own a selection")
		})
	}
}

// TestSetCursorAtScroll pins clamping and the pending-position path.
func TestSetCursorAtScroll(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		want    term.Coordinates
	}{
		{name: "in bounds", content: "abc\ndef",
			at: term.Coordinates{X: 1, Y: 1}, want: term.Coordinates{X: 1, Y: 1}},
		{name: "clamps a negative column", content: "abc",
			at: term.Coordinates{X: -5}, want: term.Coordinates{}},
		{name: "clamps a negative row", content: "abc",
			at: term.Coordinates{Y: -5}, want: term.Coordinates{}},
		{name: "clamps a row past the end", content: "abc\ndef",
			at: term.Coordinates{Y: 99}, want: term.Coordinates{Y: 1}},
		{name: "clamps a column past the end", content: "abc",
			at: term.Coordinates{X: 99}, want: term.Coordinates{X: 3}},
		{name: "clamps both", content: "ab\ncd",
			at: term.Coordinates{X: 99, Y: 99}, want: term.Coordinates{X: 2, Y: 1}},
		{name: "an empty line pins column zero", content: "a\n\nb",
			at: term.Coordinates{X: 4, Y: 1}, want: term.Coordinates{Y: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, _, _ := newHelix(t, tc.content, term.Coordinates{})
			hx.SetCursorAtScroll(tc.at)
			assert.Equal(t, tc.want, hx.CursorAtScroll())
		})
	}

	t.Run("a position set before the first resize is applied later", func(t *testing.T) {
		resource, err := workspaceapi.ParseURI("test:///")
		require.NoError(t, err)
		buf := cell.NewBuffer()
		buf.ReadFrom(strings.NewReader("one\ntwo\nthree"))
		hx := NewWithIndent(buf, resource, text.IndentRuneSpace, 2,
			WithClipboard(registerset.New(clipboard.NewInMemory())))

		assert.False(t, hx.SetCursorAtScroll(term.Coordinates{X: 1, Y: 2}),
			"the scroll has no size yet")
		hx.Resize(80, 20)
		assert.Equal(t, term.Coordinates{X: 1, Y: 2}, hx.CursorAtScroll())
		_, ok := hx.cursor.SelectionMode()
		assert.True(t, ok, "resize restores the selection invariant")
	})

	t.Run("a user event cancels a pending position", func(t *testing.T) {
		resource, err := workspaceapi.ParseURI("test:///")
		require.NoError(t, err)
		buf := cell.NewBuffer()
		buf.ReadFrom(strings.NewReader("one\ntwo\nthree"))
		hx := NewWithIndent(buf, resource, text.IndentRuneSpace, 2,
			WithClipboard(registerset.New(clipboard.NewInMemory())))

		hx.SetCursorAtScroll(term.Coordinates{X: 1, Y: 2})
		hx.Handle(key('l'))
		hx.Resize(80, 20)
		assert.Equal(t, 0, hx.CursorAtScroll().Y,
			"the pending position was dropped by the keystroke")
	})
}

// TestMouse pins click and drag, which is the only way into select mode
// that does not go through v.
func TestMouse(t *testing.T) {
	click := func(x, y int) term.Event {
		return term.Event{Type: term.EventMouse, Key: term.MouseLeft,
			MouseX: x, MouseY: y}
	}
	release := func(x, y int) term.Event {
		return term.Event{Type: term.EventMouse, Key: term.MouseRelease,
			MouseX: x, MouseY: y}
	}

	t.Run("a click moves the caret", func(t *testing.T) {
		hx, _, _ := newHelix(t, "one\ntwo\nthree", term.Coordinates{})
		send(t, hx, click(2, 1), release(2, 1))
		assert.Equal(t, term.Coordinates{X: 2, Y: 1}, hx.CursorAtScroll())
		assert.False(t, hx.IsSelectMode())
	})

	t.Run("a drag enters select mode", func(t *testing.T) {
		hx, _, _ := newHelix(t, "one\ntwo\nthree", term.Coordinates{})
		send(t, hx, click(0, 0), click(2, 0), release(2, 0))
		assert.True(t, hx.IsSelectMode(), "a drag leaves the editor extending")
		assert.Equal(t, "one", sel(t, hx))
	})

	t.Run("a following motion extends the drag selection", func(t *testing.T) {
		hx, _, _ := newHelix(t, "one two", term.Coordinates{})
		send(t, hx, click(0, 0), click(2, 0), release(2, 0))
		send(t, hx, key('l'))
		assert.Equal(t, "one ", sel(t, hx))
	})

	t.Run("a wheel event is handled", func(t *testing.T) {
		hx, _, _ := newHelix(t, strings.Repeat("line\n", 100), term.Coordinates{})
		_, handled := hx.Handle(term.Event{Type: term.EventMouse,
			Key: term.MouseWheelDown})
		assert.True(t, handled)
	})
}

// TestLocationLists pins the location plumbing the IDE drives.
func TestLocationLists(t *testing.T) {
	locs := textapi.LocationSlice([]textapi.Location{
		{From: term.Coordinates{Y: 0}, To: term.Coordinates{X: 1, Y: 0}},
		{From: term.Coordinates{Y: 2}, To: term.Coordinates{X: 1, Y: 2}},
	})

	t.Run("next and previous walk the list", func(t *testing.T) {
		hx, _, _ := newHelix(t, "a\nb\nc", term.Coordinates{})
		hx.SetLocationList(textapi.LocationPriorityError, "diag", locs)
		require.True(t, hx.MoveToNextLocation("diag"))
		assert.Equal(t, 2, hx.CursorAtScroll().Y)
		require.True(t, hx.MoveToPrevLocation("diag"))
		assert.Equal(t, 0, hx.CursorAtScroll().Y)
	})

	t.Run("an unknown list is inert", func(t *testing.T) {
		hx, _, _ := newHelix(t, "a\nb\nc", term.Coordinates{})
		assert.False(t, hx.MoveToNextLocation("nope"))
	})

	t.Run("the lists are reported back", func(t *testing.T) {
		hx, _, _ := newHelix(t, "a\nb\nc", term.Coordinates{})
		hx.SetLocationList(textapi.LocationPriorityError, "diag", locs)
		var ids []string
		for _, set := range hx.LocationLists() {
			ids = append(ids, set.ID)
		}
		assert.Contains(t, ids, "diag")
	})

	t.Run("g. jumps to the last change", func(t *testing.T) {
		hx, _, _ := newHelix(t, "a\nb\nc", term.Coordinates{Y: 2})
		send(t, hx, key('d'))
		send(t, hx, keys("gg")...)
		require.Equal(t, 0, hx.CursorAtScroll().Y)
		send(t, hx, keys("g.")...)
		assert.Equal(t, 2, hx.CursorAtScroll().Y)
	})
}

// TestDrawAndDimensions exercises the component surface.
func TestDrawAndDimensions(t *testing.T) {
	t.Run("draws the buffer", func(t *testing.T) {
		hx, _, _ := newHelix(t, "one\ntwo", term.Coordinates{})
		hx.Resize(10, 4)
		w := term.NewStringWriter(10, 4)
		hx.Draw(w)
		require.NoError(t, w.Flush())
		assert.Contains(t, w.String(), "one")
		assert.Contains(t, w.String(), "two")
	})

	t.Run("dimensions follow the content", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abcdef\nxy", term.Coordinates{})
		width, height := hx.Dimensions()
		assert.Equal(t, 6, width)
		assert.Equal(t, 2, height)
	})

	t.Run("the cursor style tracks the mode", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		_, style, visible := hx.Cursor()
		require.True(t, visible)
		assert.Equal(t, term.CursorStyleDefault, style)

		send(t, hx, key('i'))
		_, style, _ = hx.Cursor()
		assert.Equal(t, term.CursorStyleSteadyBar, style)

		send(t, hx, namedKey(term.KeyEsc))
		send(t, hx, key('g'))
		_, style, _ = hx.Cursor()
		assert.Equal(t, term.CursorStyleSteadyUnderline, style)
	})

	t.Run("close is idempotent", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		hx.Close()
		hx.Close()
	})

	t.Run("a zero sized resize is survivable", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		hx.Resize(0, 0)
		hx.Resize(80, 20)
		send(t, hx, key('l'))
		assert.Equal(t, term.Coordinates{X: 1}, hx.CursorAtScroll())
	})

	t.Run("wrap can be toggled", func(t *testing.T) {
		hx, _, _ := newHelix(t, strings.Repeat("x", 200), term.Coordinates{})
		hx.Resize(20, 10)
		hx.SetWrap(true)
		hx.Draw(term.NewStringWriter(20, 10))
		hx.SetWrap(false)
		hx.Draw(term.NewStringWriter(20, 10))
	})
}

// TestEmptyAndDegenerateBuffers walks every command over buffers that
// have nothing, or almost nothing, to act on.
func TestEmptyAndDegenerateBuffers(t *testing.T) {
	contents := []string{"", "\n", "\n\n\n", "a", "\t", " "}
	// One representative key from every dispatch arm.
	events := []term.Event{
		key('h'), key('j'), key('k'), key('l'),
		key('w'), key('W'), key('e'), key('E'), key('b'), key('B'),
		key('x'), key('X'), key('%'), key('v'), key(';'),
		key('d'), key('c'), key('y'), key('p'), key('P'), key('R'),
		key('~'), key('`'), key('J'), key('>'), key('<'), key('='),
		key('n'), key('N'), key('G'), key('u'), key('U'), key('.'),
		namedKey(term.KeyHome), namedKey(term.KeyEnd),
		namedKey(term.KeyPgup), namedKey(term.KeyPgdn),
		namedKey(term.KeyArrowUp), namedKey(term.KeyArrowDown),
		namedKey(term.KeyArrowLeft), namedKey(term.KeyArrowRight),
		namedKey(term.KeyTab), namedKey(term.KeyEsc),
		modKey(term.ModCtrl, 'a'), modKey(term.ModCtrl, 'x'),
		modKey(term.ModCtrl, 'b'), modKey(term.ModCtrl, 'f'),
		modKey(term.ModCtrl, 'u'), modKey(term.ModCtrl, 'd'),
		modKey(term.ModCtrl, 'e'), modKey(term.ModCtrl, 'y'),
		modKey(term.ModCtrl, 's'), modKey(term.ModCtrl, 'o'),
		modKey(term.ModCtrl, 'i'), modKey(term.ModCtrl, 'c'),
		modKey(term.ModAlt, 'd'), modKey(term.ModAlt, 'c'),
		modKey(term.ModAlt, ';'), modKey(term.ModAlt, ':'),
		modKey(term.ModAlt, 'x'), modKey(term.ModAlt, 'o'),
		modKey(term.ModAlt, 'i'), modKey(term.ModAlt, '`'),
		modKey(term.ModAlt, '.'), modKey(term.ModAlt, 'J'),
		modKey(term.ModAlt, '*'),
	}
	// Minor modes reached through a prefix key.
	prefixes := [][]term.Event{
		{key('g'), key('g')}, {key('g'), key('e')}, {key('g'), key('h')},
		{key('g'), key('l')}, {key('g'), key('s')}, {key('g'), key('|')},
		{key('g'), key('t')}, {key('g'), key('c')}, {key('g'), key('b')},
		{key('g'), key('j')}, {key('g'), key('k')}, {key('g'), key('.')},
		{key('m'), key('m')}, {key('m'), key('i'), key('w')},
		{key('m'), key('a'), key('w')}, {key('m'), key('s'), key('(')},
		{key('m'), key('d'), key('(')}, {key('m'), key('r'), key('('), key('[')},
		{key('z'), key('z')}, {key('z'), key('t')}, {key('z'), key('b')},
		{key('['), namedKey(term.KeySpace)}, {key(']'), namedKey(term.KeySpace)},
		{key('['), key('p')}, {key(']'), key('p')},
		{key('r'), key('z')}, {key('f'), key('z')}, {key('t'), key('z')},
		{key('F'), key('z')}, {key('T'), key('z')},
		{key('"'), key('a'), key('y')},
	}

	for _, content := range contents {
		for _, ev := range events {
			hx, _, _ := newHelix(t, content, term.Coordinates{})
			require.NotPanics(t, func() { hx.Handle(ev) },
				"content %q event %v", content, ev)
		}
		for _, seq := range prefixes {
			hx, _, _ := newHelix(t, content, term.Coordinates{})
			require.NotPanics(t, func() { send(t, hx, seq...) },
				"content %q sequence %v", content, seq)
		}
	}
}

// TestOutOfBoundsCaret drives commands from positions the IDE can hand
// over after an out-of-band edit.
func TestOutOfBoundsCaret(t *testing.T) {
	positions := []term.Coordinates{
		{X: -1, Y: -1}, {X: 1000, Y: 0}, {X: 0, Y: 1000},
		{X: 1000, Y: 1000}, {X: -5, Y: 2},
	}
	for _, at := range positions {
		for _, ev := range []term.Event{
			key('w'), key('b'), key('e'), key('d'), key('x'), key('J'),
			key('%'), key('j'), key('k'), modKey(term.ModCtrl, 'a'),
		} {
			hx, _, _ := newHelix(t, "one\n\nthree", term.Coordinates{})
			hx.SetCursorAtScroll(at)
			require.NotPanics(t, func() { hx.Handle(ev) },
				"at %v event %v", at, ev)
			pos := hx.CursorAtScroll()
			assert.GreaterOrEqual(t, pos.X, 0)
			assert.GreaterOrEqual(t, pos.Y, 0)
		}
	}
}

// TestWideAndControlCharacters pins the motions over content the cell
// grid stores in more than one column, or not at all.
func TestWideAndControlCharacters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		evs     []term.Event
		wantSel string
	}{
		{name: "l steps one glyph at a time", content: "世界x",
			evs: keys("l"), wantSel: "界"},
		{name: "w over CJK", content: "世界 x", evs: keys("w"), wantSel: "世界 "},
		{name: "d removes a whole glyph", content: "世界", evs: keys("d")},
		{name: "a tab is one cell", content: "a\tb", evs: keys("l"),
			wantSel: "\t"},
		{name: "w over a tab", content: "a\tb", evs: keys("w"), wantSel: "a\t"},
		{name: "e over mixed scripts", content: "héllo wörld", evs: keys("e"),
			wantSel: "héllo"},
		{name: "w over an emoji", content: "a 🙂 b", evs: keys("w"), wantSel: "a "},
		{name: "% selects everything including wide glyphs",
			content: "世界\nxy", evs: keys("%"), wantSel: "世界\nxy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, _, _ := newHelix(t, tc.content, term.Coordinates{})
			send(t, hx, tc.evs...)
			if tc.wantSel != "" {
				assert.Equal(t, tc.wantSel, sel(t, hx))
			}
		})
	}

	t.Run("a NUL byte does not derail a motion", func(t *testing.T) {
		hx, _, _ := newHelix(t, "a\x00b c", term.Coordinates{})
		require.NotPanics(t, func() { send(t, hx, keys("wwbb")...) })
	})

	t.Run("very long lines are navigable", func(t *testing.T) {
		hx, _, _ := newHelix(t, strings.Repeat("word ", 500), term.Coordinates{})
		send(t, hx, keys("100w")...)
		assert.Greater(t, hx.CursorAtScroll().X, 0)
	})
}

// TestLargeCounts pins counts that overrun the buffer.
func TestLargeCounts(t *testing.T) {
	for _, evs := range [][]term.Event{
		keys("999j"), keys("999k"), keys("999l"), keys("999h"),
		keys("999w"), keys("999b"), keys("999e"), keys("999x"),
		keys("999>"), keys("999<"), keys("999G"),
	} {
		hx, _, _ := newHelix(t, "one\ntwo\nthree", term.Coordinates{Y: 1})
		require.NotPanics(t, func() { send(t, hx, evs...) }, "%v", evs)
		pos := hx.CursorAtScroll()
		assert.Less(t, pos.Y, 3)
		assert.GreaterOrEqual(t, pos.Y, 0)
	}

	t.Run("a multi digit count accumulates", func(t *testing.T) {
		hx, _, _ := newHelix(t, strings.Repeat("x\n", 300), term.Coordinates{})
		send(t, hx, keys("123j")...)
		assert.Equal(t, 123, hx.CursorAtScroll().Y)
	})

	t.Run("a leading zero is not a count", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{X: 2})
		_, handled := hx.Handle(key('0'))
		assert.False(t, handled, "0 is goto-line-start in Helix's gh, not a count")
	})

	t.Run("the count resets after the command", func(t *testing.T) {
		hx, _, _ := newHelix(t, strings.Repeat("x\n", 20), term.Coordinates{})
		send(t, hx, keys("3j")...)
		require.Equal(t, 3, hx.CursorAtScroll().Y)
		send(t, hx, key('j'))
		assert.Equal(t, 4, hx.CursorAtScroll().Y)
	})

	t.Run("an abandoned count does not leak", func(t *testing.T) {
		hx, _, _ := newHelix(t, strings.Repeat("x\n", 20), term.Coordinates{})
		send(t, hx, key('3'))
		send(t, hx, namedKey(term.KeyEsc))
		send(t, hx, key('j'))
		assert.Equal(t, 1, hx.CursorAtScroll().Y)
	})
}

// TestNonKeyEvents pins the events the runtime delivers that are not
// keystrokes.
func TestNonKeyEvents(t *testing.T) {
	for _, ev := range []term.Event{
		{Type: term.EventResize},
		{Type: term.EventError},
		{Type: term.EventInterrupt},
	} {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		require.NotPanics(t, func() { hx.Handle(ev) })
		assert.Equal(t, "abc", buf.String())
	}

	t.Run("a zero key event in normal mode is unhandled", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		_, handled := hx.Handle(term.Event{Type: term.EventKey})
		assert.False(t, handled)
	})

	t.Run("a zero key event does not resolve a pending prefix", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('r'), term.Event{Type: term.EventKey})
		assert.Equal(t, "abc", buf.String())
		assert.True(t, hx.IsNormalMode())
	})
}

// TestSeekSurface pins the scrollable API the IDE reads.
func TestSeekSurface(t *testing.T) {
	hx, _, _ := newHelix(t, strings.Repeat("line\n", 200), term.Coordinates{})
	assert.Equal(t, 0, hx.SeekOffset())
	assert.Greater(t, hx.MaxSeekOffset(), 0)

	hx.SeekDown()
	assert.Equal(t, 1, hx.SeekOffset())
	hx.SeekUp()
	assert.Equal(t, 0, hx.SeekOffset())
}

// TestConstructors pins the entry points the IDE uses to build a Helix
// editor, including the Scroll-backed variant used by embedded views.
func TestConstructors(t *testing.T) {
	resource, err := workspaceapi.ParseURI("test:///main.go")
	require.NoError(t, err)

	t.Run("New defaults to tab indentation", func(t *testing.T) {
		buf := cell.NewBuffer()
		buf.ReadFrom(strings.NewReader("abc"))
		hx := New(buf, resource)
		hx.Resize(80, 20)
		assert.Equal(t, text.IndentRuneTab, hx.config.indentRune)
		assert.True(t, hx.IsNormalMode())
		send(t, hx, key('l'))
		assert.Equal(t, term.Coordinates{X: 1}, hx.CursorAtScroll())
	})

	t.Run("InitWithScroll shares an existing scroll", func(t *testing.T) {
		buf := cell.NewBuffer()
		buf.ReadFrom(strings.NewReader("one\ntwo"))
		scroll := component.NewScroll(buf)

		hx := new(Helix)
		hx.InitWithScroll(scroll, resource, text.IndentRuneSpace, 2)
		hx.Resize(80, 20)
		send(t, hx, key('j'))
		assert.Equal(t, term.Coordinates{Y: 1}, hx.CursorAtScroll())
	})

	t.Run("initial folds are requested when enabled", func(t *testing.T) {
		buf := cell.NewBuffer()
		buf.ReadFrom(strings.NewReader("one\ntwo"))
		// The plain view exposes no folds service, so this only has to
		// stay inert.
		hx := NewWithIndent(buf, resource, text.IndentRuneSpace, 2,
			WithHideInitialFolds(true))
		hx.Resize(80, 20)
		assert.True(t, hx.IsNormalMode())
	})
}

// TestScrollSubscriber pins the callbacks component.Scroll delivers when
// folds open and close under the caret.
func TestScrollSubscriber(t *testing.T) {
	hx, _, _ := newHelix(t, strings.Repeat("line\n", 40), term.Coordinates{Y: 5})
	impl := hx.handler.(*helixHandlerImpl)

	require.NotPanics(t, func() {
		impl.OnWillSeek(term.Coordinates{})
		impl.OnDidSeek(term.Coordinates{}, term.Coordinates{Y: 1})
		impl.OnWillHide(1, 3)
		impl.OnDidHide(1, 3)
		impl.OnWillVisible(1)
		impl.OnDidVisible(1)
	})
	assert.Equal(t, hx.CursorAtScroll(), impl.anchor,
		"the desired column is resynced after a fold change")
}

// TestMouseInInsertMode pins the click path that bypasses the selection
// delegate.
func TestMouseInInsertMode(t *testing.T) {
	hx, buf, _ := newHelix(t, "one\ntwo", term.Coordinates{})
	send(t, hx, key('i'))
	require.True(t, hx.IsEditMode())

	send(t, hx, term.Event{Type: term.EventMouse, Key: term.MouseLeft,
		MouseX: 1, MouseY: 1})
	send(t, hx, term.Event{Type: term.EventMouse, Key: term.MouseRelease,
		MouseX: 1, MouseY: 1})
	assert.True(t, hx.IsEditMode(), "a click does not leave insert mode")
	send(t, hx, key('X'))
	assert.Equal(t, "one\ntXwo", buf.String())
}
