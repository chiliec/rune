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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/unstablebuild/rune-go-sdk/clipboard"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/text"
)

// opCase drives an operator through the real handler and checks the
// resulting buffer, selection and caret.
type opCase struct {
	name    string
	content string
	at      term.Coordinates
	evs     []term.Event
	want    string
	wantSel string
	wantAt  *term.Coordinates
}

func runOpCases(t *testing.T, cases []opCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hx, buf, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.want, buf.String(), "buffer")
			if tc.wantSel != "" {
				assert.Equal(t, tc.wantSel, sel(t, hx), "selection")
			}
			if tc.wantAt != nil {
				assert.Equal(t, *tc.wantAt, hx.CursorAtScroll(), "caret")
			}
		})
	}
}

func at(x, y int) *term.Coordinates { return &term.Coordinates{X: x, Y: y} }

// TestDelete pins d and A-d.
func TestDelete(t *testing.T) {
	runOpCases(t, []opCase{
		{name: "d removes the one-cell selection", content: "abc",
			evs: keys("d"), want: "bc", wantAt: at(0, 0)},
		{name: "d removes a word selection", content: "foo bar",
			evs: keys("wd"), want: "bar"},
		{name: "d removes a line selection", content: "one\ntwo\nthree",
			evs: keys("xd"), want: "two\nthree"},
		{name: "d over several lines", content: "one\ntwo\nthree",
			evs: keys("xxd"), want: "three"},
		{name: "d at the buffer end", content: "ab", at: term.Coordinates{X: 1},
			evs: keys("d"), want: "a"},
		{name: "d on an empty buffer is inert", content: "", evs: keys("d"), want: ""},
		{name: "d on a wide glyph removes the whole rune", content: "世界",
			evs: keys("d"), want: "界"},
		{name: "alt-d deletes without yanking", content: "foo bar",
			evs: append(keys("w"), modKey(term.ModAlt, 'd')), want: "bar"},
		{name: "d drops select mode", content: "foo bar",
			evs: keys("vwd"), want: "bar"},
		{name: "counted d still deletes the selection once", content: "abcd",
			evs: keys("3d"), want: "bcd"},
	})

	t.Run("d yanks into the unnamed register", func(t *testing.T) {
		hx, _, clip := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("wd")...)
		data, err := clip.Paste(clipboard.DefaultRegisterID)
		require.NoError(t, err)
		assert.Equal(t, "foo ", data.Text)
	})

	t.Run("alt-d leaves the register alone", func(t *testing.T) {
		hx, _, clip := newHelix(t, "foo bar", term.Coordinates{})
		seedClipboard(t, clip, "KEEP", text.StandardSelection)
		send(t, hx, key('w'))
		send(t, hx, modKey(term.ModAlt, 'd'))
		data, err := clip.Paste(clipboard.DefaultRegisterID)
		require.NoError(t, err)
		assert.Equal(t, "KEEP", data.Text)
	})

	t.Run("the black hole register discards the text", func(t *testing.T) {
		hx, _, clip := newHelix(t, "foo bar", term.Coordinates{})
		seedClipboard(t, clip, "KEEP", text.StandardSelection)
		send(t, hx, keys("w\"_d")...)
		data, err := clip.Paste(clipboard.DefaultRegisterID)
		require.NoError(t, err)
		assert.Equal(t, "KEEP", data.Text)
	})
}

// TestChange pins c and A-c, which delete and then enter insert mode.
func TestChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   term.Event
	}{
		{name: "c", ev: key('c')},
		{name: "alt-c", ev: modKey(term.ModAlt, 'c')},
	} {
		t.Run(tc.name+" deletes and enters insert", func(t *testing.T) {
			hx, buf, _ := newHelix(t, "foo bar", term.Coordinates{})
			send(t, hx, key('w'), tc.ev)
			require.True(t, hx.IsEditMode())
			send(t, hx, keys("X")...)
			assert.Equal(t, "Xbar", buf.String())
		})
	}

	t.Run("c on an empty buffer still enters insert", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "", term.Coordinates{})
		send(t, hx, key('c'))
		assert.True(t, hx.IsEditMode())
		send(t, hx, keys("hi")...)
		assert.Equal(t, "hi", buf.String())
	})

	// delete_selection_impl opens a line instead of plainly entering
	// insert mode when the selection covers whole lines, so xc leaves a
	// blank line to type on rather than pulling the next one up.
	t.Run("xc leaves a blank line behind", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "foo\nbar", term.Coordinates{})
		send(t, hx, keys("xc")...)
		require.True(t, hx.IsEditMode())
		assert.Equal(t, "\nbar", buf.String())
		assert.Equal(t, term.Coordinates{}, hx.CursorAtScroll())
		send(t, hx, keys("hi")...)
		assert.Equal(t, "hi\nbar", buf.String())
	})

	t.Run("xxc replaces both lines with one blank line", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "foo\nbar\nbaz", term.Coordinates{})
		send(t, hx, keys("xxc")...)
		assert.Equal(t, "\nbaz", buf.String())
	})

	t.Run("alt-c is linewise too", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "foo\nbar", term.Coordinates{})
		send(t, hx, key('x'), modKey(term.ModAlt, 'c'))
		assert.Equal(t, "\nbar", buf.String())
	})
}

// TestYankAndPaste pins y, p, P and R.
func TestYankAndPaste(t *testing.T) {
	t.Run("y copies without deleting", func(t *testing.T) {
		hx, buf, clip := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("wy")...)
		assert.Equal(t, "foo bar", buf.String())
		data, err := clip.Paste(clipboard.DefaultRegisterID)
		require.NoError(t, err)
		assert.Equal(t, "foo ", data.Text)
		assert.Equal(t, "foo ", sel(t, hx), "the selection survives a yank")
	})

	t.Run("y also fills register 0", func(t *testing.T) {
		hx, _, clip := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("wy")...)
		data, err := clip.Paste(registerNameToID('0'))
		require.NoError(t, err)
		assert.Equal(t, "foo ", data.Text)
	})

	t.Run("a named register keeps its own copy", func(t *testing.T) {
		hx, _, clip := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("w\"ay")...)
		data, err := clip.Paste(registerNameToID('a'))
		require.NoError(t, err)
		assert.Equal(t, "foo ", data.Text)
	})

	t.Run("y with no selection is unhandled", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		require.True(t, hx.Unselect())
		_, handled := hx.Handle(key('y'))
		assert.False(t, handled)
	})

	// Helix's p/P never replace the selection; only R does.
	for _, tc := range []struct {
		name string
		evs  []term.Event
		want string
	}{
		{name: "p pastes after the selection", evs: keys("wp"), want: "foo Xbar"},
		{name: "P pastes before the selection", evs: keys("wP"), want: "Xfoo bar"},
		{name: "R replaces the selection", evs: keys("wR"), want: "Xbar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, buf, clip := newHelix(t, "foo bar", term.Coordinates{})
			seedClipboard(t, clip, "X", text.StandardSelection)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.want, buf.String())
		})
	}

	t.Run("counted p repeats the paste", func(t *testing.T) {
		hx, buf, clip := newHelix(t, "ab", term.Coordinates{})
		seedClipboard(t, clip, "X", text.StandardSelection)
		send(t, hx, keys("3p")...)
		assert.Equal(t, "aXXXb", buf.String())
	})

	t.Run("linewise paste lands on its own line", func(t *testing.T) {
		hx, buf, clip := newHelix(t, "one\ntwo", term.Coordinates{})
		seedClipboard(t, clip, "mid\n", text.LineSelection)
		send(t, hx, key('p'))
		assert.Equal(t, "one\nmid\ntwo", buf.String())
	})

	t.Run("p from an empty register is unhandled", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		_, handled := hx.Handle(key('p'))
		assert.False(t, handled)
	})

	t.Run("yank then paste round trips", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("wy")...)
		send(t, hx, key('p'))
		assert.Equal(t, "foo foo bar", buf.String())
	})
}

// TestReplaceChar pins r<char>, which rewrites every selected cell.
func TestReplaceChar(t *testing.T) {
	runOpCases(t, []opCase{
		{name: "r replaces the cell under the caret", content: "abc",
			evs: keys("rz"), want: "zbc", wantSel: "z"},
		{name: "r replaces the whole selection", content: "foo bar",
			evs: keys("wrz"), want: "zzzzbar", wantSel: "zzzz"},
		{name: "r over a line selection", content: "ab\ncd",
			evs: keys("xr-"), want: "--\ncd"},
		{name: "r with a wide glyph", content: "世界",
			evs: keys("rx"), want: "x界"},
		{name: "r on a wide replacement", content: "ab",
			evs: append(keys("r"), key('界')), want: "界b"},
	})

	t.Run("r<space> uses a literal space", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('r'), namedKey(term.KeySpace))
		assert.Equal(t, " bc", buf.String())
	})

	t.Run("r<tab> uses a literal tab", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('r'), namedKey(term.KeyTab))
		assert.Equal(t, "\tbc", buf.String())
	})

	t.Run("r<enter> splits the line", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('r'), namedKey(term.KeyEnter))
		assert.Equal(t, "\nbc", buf.String())
	})

	t.Run("esc cancels replace mode", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('r'), namedKey(term.KeyEsc))
		assert.Equal(t, "abc", buf.String())
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("r is a one-shot mode", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, keys("rzz")...)
		assert.Equal(t, "zbc", buf.String(), "the second z is a normal-mode key")
	})

	t.Run("r on an empty buffer is inert", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "", term.Coordinates{})
		send(t, hx, keys("rz")...)
		assert.Equal(t, "", buf.String())
	})
}

// TestCaseOperators pins ~, ` and A-`.
func TestCaseOperators(t *testing.T) {
	runOpCases(t, []opCase{
		{name: "~ toggles a mixed selection", content: "aBc",
			evs: keys("w~"), want: "AbC"},
		{name: "~ toggles one cell", content: "abc", evs: keys("~"),
			want: "Abc", wantSel: "A"},
		{name: "backtick lowercases", content: "ABC",
			evs: keys("w`"), want: "abc"},
		{name: "alt-backtick uppercases", content: "abc",
			evs: append(keys("w"), modKey(term.ModAlt, '`')), want: "ABC"},
		{name: "case operators keep the selection", content: "abc",
			evs: keys("w~"), want: "ABC", wantSel: "ABC"},
		{name: "~ over wide glyphs is a no-op", content: "世界",
			evs: keys("w~"), want: "世界"},
	})
}

// TestJoin pins J and A-J.
func TestJoin(t *testing.T) {
	runOpCases(t, []opCase{
		{name: "J joins the next line", content: "one\ntwo",
			evs: keys("J"), want: "one two"},
		{name: "J across a line selection", content: "a\nb\nc",
			evs: keys("xxJ"), want: "a b\nc"},
		{name: "J at the last line is inert", content: "one",
			evs: keys("J"), want: "one"},
		{name: "counted J joins several lines", content: "a\nb\nc\nd",
			evs: keys("3xJ"), want: "a b c\nd"},
	})

	t.Run("alt-J selects the inserted space", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "one\ntwo", term.Coordinates{})
		send(t, hx, modKey(term.ModAlt, 'J'))
		assert.Equal(t, "one two", buf.String())
		assert.Equal(t, " ", sel(t, hx))
	})

	t.Run("J on an empty buffer is unhandled", func(t *testing.T) {
		hx, _, _ := newHelix(t, "", term.Coordinates{})
		_, handled := hx.Handle(key('J'))
		assert.False(t, handled)
	})
}

// TestIndentOperators pins >, < and =.
func TestIndentOperators(t *testing.T) {
	runOpCases(t, []opCase{
		{name: "> indents the line", content: "a\nb", evs: keys(">"), want: "  a\nb"},
		{name: "counted > indents repeatedly", content: "a", evs: keys("3>"),
			want: "      a"},
		{name: "< unindents", content: "    a", evs: keys("<"), want: "  a"},
		{name: "< on a flush line is inert", content: "a", evs: keys("<"), want: "a"},
		{name: "> across a multi line selection", content: "a\nb\nc",
			evs: keys("xx>"), want: "  a\n  b\nc"},
	})

	t.Run("= reindents to the syntax target", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "      a", term.Coordinates{})
		buf.WithView(testIndentView{View: buf.View(), indents: map[int]int{0: 0}})
		send(t, hx, key('='))
		assert.Equal(t, "a", buf.String())
	})

	t.Run("indent keeps a line selection", func(t *testing.T) {
		hx, _, _ := newHelix(t, "a\nb\nc", term.Coordinates{})
		send(t, hx, keys("xx>")...)
		assert.Equal(t, "  a\n  b\n", sel(t, hx))
	})
}

// TestComments pins C-c.

// TestTrimSelection pins _, which shrinks the selection past the
// whitespace at either end.
func TestTrimSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		evs     []term.Event
		wantSel string
	}{
		{name: "drops a trailing space", content: "foo bar",
			evs: keys("w_"), wantSel: "foo"},
		{name: "drops leading whitespace", content: "  foo",
			evs: keys("vlll_"), wantSel: "fo"},
		{name: "drops both ends", content: " ab ",
			evs: keys("%_"), wantSel: "ab"},
		{name: "spans lines", content: "a\n  b  \nc",
			evs: keys("x_"), wantSel: "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, _, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.wantSel, sel(t, hx))
		})
	}

	t.Run("a selection with no whitespace is unhandled", func(t *testing.T) {
		hx, _, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, key('e'))
		require.Equal(t, "foo", sel(t, hx))
		_, handled := hx.Handle(key('_'))
		assert.False(t, handled)
		assert.Equal(t, "foo", sel(t, hx))
	})

	t.Run("an all whitespace selection collapses", func(t *testing.T) {
		hx, _, _ := newHelix(t, "  a", term.Coordinates{})
		send(t, hx, key('w'))
		require.Equal(t, "  ", sel(t, hx))
		send(t, hx, key('_'))
		assert.Equal(t, " ", sel(t, hx))
	})

	t.Run("a backward selection keeps its direction", func(t *testing.T) {
		hx, _, _ := newHelix(t, "foo bar", term.Coordinates{X: 4})
		send(t, hx, key('v'), key('b'))
		require.Equal(t, "foo b", sel(t, hx))
		send(t, hx, key('_'))
		assert.Equal(t, "foo b", sel(t, hx), "neither end is blank")
	})
}
func TestComments(t *testing.T) {
	t.Run("ctrl-c toggles a line comment", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "a\nb", term.Coordinates{})
		hx.cursor.SetCommentSpec(text.CommentSpec{Line: []string{"//"}})
		buf.WithView(testCommentView{View: buf.View(), line: []string{"//"}})
		send(t, hx, modKey(term.ModCtrl, 'c'))
		assert.Equal(t, "// a\nb", buf.String())
		send(t, hx, modKey(term.ModCtrl, 'c'))
		assert.Equal(t, "a\nb", buf.String())
	})

	t.Run("ctrl-c across a selection", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "a\nb\nc", term.Coordinates{})
		hx.cursor.SetCommentSpec(text.CommentSpec{Line: []string{"//"}})
		send(t, hx, keys("xx")...)
		send(t, hx, modKey(term.ModCtrl, 'c'))
		assert.Equal(t, "// a\n// b\nc", buf.String())
	})
}

// TestInsertEntry pins i, a, I, A, o and O.
func TestInsertEntry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		evs     []term.Event
		want    string
	}{
		{name: "i inserts before the selection", content: "foo bar",
			evs: append(keys("wi"), keys("X")...), want: "Xfoo bar"},
		{name: "a inserts after the selection", content: "foo bar",
			evs: append(keys("wa"), keys("X")...), want: "foo Xbar"},
		{name: "i at the caret", content: "abc",
			evs: append(keys("i"), keys("X")...), want: "Xabc"},
		{name: "a at the caret", content: "abc",
			evs: append(keys("a"), keys("X")...), want: "aXbc"},
		{name: "I goes to the first non blank", content: "  abc",
			at: term.Coordinates{X: 4}, evs: append(keys("I"), keys("X")...),
			want: "  Xabc"},
		{name: "A goes to the line end", content: "abc",
			evs: append(keys("A"), keys("X")...), want: "abcX"},
		{name: "o opens below", content: "a\nb",
			evs: append(keys("o"), keys("X")...), want: "a\nX\nb"},
		{name: "O opens above", content: "a\nb",
			evs: append(keys("O"), keys("X")...), want: "X\na\nb"},
		{name: "counted o opens several lines", content: "a",
			evs: append(keys("3o"), keys("X")...), want: "a\n\n\nX"},
		{name: "a at the buffer end appends", content: "ab",
			at: term.Coordinates{X: 1}, evs: append(keys("a"), keys("X")...),
			want: "abX"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, buf, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

// TestAddNewline pins [<space> and ]<space>, which do not move the caret.
func TestAddNewline(t *testing.T) {
	t.Run("]<space> adds a line below", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "a\nb", term.Coordinates{})
		send(t, hx, key(']'), namedKey(term.KeySpace))
		assert.Equal(t, "a\n\nb", buf.String())
		assert.Equal(t, term.Coordinates{}, hx.CursorAtScroll())
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("[<space> adds a line above", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "a\nb", term.Coordinates{})
		send(t, hx, key('['), namedKey(term.KeySpace))
		assert.Equal(t, "\na\nb", buf.String())
		assert.Equal(t, term.Coordinates{Y: 1}, hx.CursorAtScroll())
	})

	t.Run("counted ]<space>", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "a", term.Coordinates{})
		send(t, hx, keys("2]")...)
		send(t, hx, namedKey(term.KeySpace))
		assert.Equal(t, "a\n\n", buf.String())
	})
}

// TestSurround pins ms, mr and md.
func TestSurround(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		evs     []term.Event
		want    string
		wantSel string
	}{
		{name: "ms wraps the selection in parens", content: "foo bar",
			evs: keys("wms("), want: "(foo )bar", wantSel: "(foo )"},
		{name: "ms with the closing key uses the same pair", content: "ab",
			evs: keys("ms)"), want: "(a)b"},
		{name: "ms with braces", content: "ab", evs: keys("ms{"), want: "{a}b"},
		{name: "ms with brackets", content: "ab", evs: keys("ms["), want: "[a]b"},
		{name: "ms with angle brackets", content: "ab", evs: keys("ms<"), want: "<a>b"},
		{name: "ms with a quote surrounds with itself", content: "ab",
			evs: keys("ms\""), want: "\"a\"b"},
		{name: "md removes the pair", content: "(foo)", at: term.Coordinates{X: 2},
			evs: keys("md("), want: "foo"},
		{name: "md with quotes", content: "\"foo\"", at: term.Coordinates{X: 2},
			evs: keys("md\""), want: "foo"},
		{name: "mr swaps the pair", content: "(foo)", at: term.Coordinates{X: 2},
			evs: keys("mr(["), want: "[foo]"},
		{name: "mr from braces to parens", content: "{foo}",
			at: term.Coordinates{X: 2}, evs: keys("mr{("), want: "(foo)"},
		{name: "md with no surrounding pair is inert", content: "foo",
			evs: keys("md("), want: "foo"},
		{name: "mr with no surrounding pair is inert", content: "foo",
			evs: keys("mr(["), want: "foo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, buf, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.want, buf.String(), "buffer")
			assert.True(t, hx.IsNormalMode(), "surround is a one shot mode")
			if tc.wantSel != "" {
				assert.Equal(t, tc.wantSel, sel(t, hx), "selection")
			}
		})
	}

	t.Run("esc cancels a pending surround", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "ab", term.Coordinates{})
		send(t, hx, keys("ms")...)
		send(t, hx, namedKey(term.KeyEsc))
		assert.Equal(t, "ab", buf.String())
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("mr waits for two delimiters", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "(foo)", term.Coordinates{X: 2})
		send(t, hx, keys("mr(")...)
		assert.False(t, hx.IsNormalMode(), "still waiting for the target pair")
		send(t, hx, key('{'))
		assert.Equal(t, "{foo}", buf.String())
	})

	t.Run("ms across lines wraps the whole span", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "one\ntwo", term.Coordinates{})
		send(t, hx, keys("xxms(")...)
		assert.Equal(t, "(one\ntwo)", buf.String())
	})

	t.Run("md on an empty pair leaves the caret in place", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "f()", term.Coordinates{X: 1})
		send(t, hx, keys("md(")...)
		assert.Equal(t, "f", buf.String())
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("a surround delimiter cannot be a modified key", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "ab", term.Coordinates{})
		send(t, hx, keys("ms")...)
		_, handled := hx.Handle(modKey(term.ModCtrl, 'g'))
		assert.False(t, handled)
		assert.Equal(t, "ab", buf.String())
	})
}

// TestIncrement pins C-a and C-x.
func TestIncrement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		evs     []term.Event
		want    string
	}{
		{name: "ctrl-a increments", content: "1",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: "2"},
		{name: "ctrl-x decrements", content: "2",
			evs: []term.Event{modKey(term.ModCtrl, 'x')}, want: "1"},
		{name: "counted increment", content: "1",
			evs: append(keys("5"), modKey(term.ModCtrl, 'a')), want: "6"},
		{name: "rolls over digits", content: "9",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: "10"},
		{name: "borrows across digits", content: "10",
			evs: []term.Event{modKey(term.ModCtrl, 'x')}, want: "9"},
		{name: "keeps zero padding", content: "007",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: "008"},
		{name: "padding survives a carry", content: "099",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: "100"},
		{name: "crosses zero into negative", content: "0",
			evs: []term.Event{modKey(term.ModCtrl, 'x')}, want: "-1"},
		{name: "negative numbers increment", content: "-2",
			at: term.Coordinates{X: 1}, evs: []term.Event{modKey(term.ModCtrl, 'a')},
			want: "-1"},
		{name: "finds the number after the caret", content: "ab 12",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: "ab 13"},
		{name: "picks up the whole run from the middle", content: "1234",
			at: term.Coordinates{X: 2}, evs: []term.Event{modKey(term.ModCtrl, 'a')},
			want: "1235"},
		{name: "no number leaves the line alone", content: "abc",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: "abc"},
		{name: "empty buffer is inert", content: "",
			evs: []term.Event{modKey(term.ModCtrl, 'a')}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, buf, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.want, buf.String())
		})
	}

	t.Run("the new digits stay selected", func(t *testing.T) {
		hx, _, _ := newHelix(t, "9", term.Coordinates{})
		send(t, hx, modKey(term.ModCtrl, 'a'))
		assert.Equal(t, "10", sel(t, hx))
	})

	t.Run("a multi line selection is rejected", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "1\n2", term.Coordinates{})
		send(t, hx, keys("xx")...)
		_, handled := hx.Handle(modKey(term.ModCtrl, 'a'))
		assert.False(t, handled)
		assert.Equal(t, "1\n2", buf.String())
	})
}

// TestTextObjects pins the mi/ma pairs.
func TestTextObjects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		at      term.Coordinates
		evs     []term.Event
		wantSel string
	}{
		{name: "miw selects the inner word", content: "foo bar",
			at: term.Coordinates{X: 5}, evs: keys("miw"), wantSel: "bar"},
		{name: "maw includes the trailing space", content: "foo bar",
			at: term.Coordinates{X: 1}, evs: keys("maw"), wantSel: "foo "},
		{name: "miW spans punctuation", content: "a.b c",
			evs: keys("miW"), wantSel: "a.b"},
		{name: "mi\" selects inside quotes", content: "x \"abc\" y",
			at: term.Coordinates{X: 4}, evs: keys("mi\""), wantSel: "abc"},
		{name: "ma\" includes the quotes", content: "x \"abc\" y",
			at: term.Coordinates{X: 4}, evs: keys("ma\""), wantSel: "\"abc\""},
		{name: "mi( selects inside parens", content: "f(ab)",
			at: term.Coordinates{X: 3}, evs: keys("mi("), wantSel: "ab"},
		{name: "ma( includes the parens", content: "f(ab)",
			at: term.Coordinates{X: 3}, evs: keys("ma("), wantSel: "(ab)"},
		{name: "mi{ selects inside braces", content: "f{ab}",
			at: term.Coordinates{X: 3}, evs: keys("mi{"), wantSel: "ab"},
		{name: "mi[ selects inside brackets", content: "f[ab]",
			at: term.Coordinates{X: 3}, evs: keys("mi["), wantSel: "ab"},
		{name: "mip selects the paragraph", content: "a\nb\n\nc",
			evs: keys("mip"), wantSel: "a\nb\n"},
		{name: "mim finds the closest pair", content: "f(ab)",
			at: term.Coordinates{X: 3}, evs: keys("mim"), wantSel: "ab"},
		{name: "mam includes the closest pair", content: "f(ab)",
			at: term.Coordinates{X: 3}, evs: keys("mam"), wantSel: "(ab)"},
		{name: "mim steps over a nested pair", content: "{a (b) c}",
			at: term.Coordinates{X: 1}, evs: keys("mim"), wantSel: "a (b) c"},
		{name: "mim picks the innermost enclosing pair", content: "{a [b] c}",
			at: term.Coordinates{X: 4}, evs: keys("mim"), wantSel: "b"},
		{name: "mim ignores brackets that shut before the caret",
			content: "(a) [b]", at: term.Coordinates{X: 5},
			evs: keys("mim"), wantSel: "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hx, _, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, tc.evs...)
			assert.Equal(t, tc.wantSel, sel(t, hx))
			assert.True(t, hx.IsNormalMode(), "mi/ma are one shot")
		})
	}

	t.Run("an unknown object leaves the selection alone", func(t *testing.T) {
		hx, _, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("wmiZ")...)
		assert.Equal(t, "foo ", sel(t, hx))
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("mim outside any pair leaves the selection alone", func(t *testing.T) {
		hx, _, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("wmim")...)
		assert.Equal(t, "foo ", sel(t, hx))
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("a text object is a delete target", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "f(ab)", term.Coordinates{X: 3})
		send(t, hx, keys("mi(d")...)
		assert.Equal(t, "f()", buf.String())
	})

	t.Run("esc cancels match mode", func(t *testing.T) {
		hx, _, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, key('w'), key('m'))
		send(t, hx, namedKey(term.KeyEsc))
		assert.True(t, hx.IsNormalMode())
		assert.Equal(t, "foo ", sel(t, hx))
	})

	t.Run("ma matches mi for every object", func(t *testing.T) {
		for _, tc := range []struct {
			content string
			at      term.Coordinates
			object  rune
			want    string
		}{
			{content: "x 'abc' y", at: term.Coordinates{X: 4}, object: '\'',
				want: "'abc'"},
			{content: "x `abc` y", at: term.Coordinates{X: 4}, object: '`',
				want: "`abc`"},
			{content: "f<ab>", at: term.Coordinates{X: 3}, object: '<',
				want: "<ab>"},
			{content: "One. Two.", at: term.Coordinates{X: 1}, object: 's',
				want: "One. "},
		} {
			hx, _, _ := newHelix(t, tc.content, tc.at)
			send(t, hx, keys("ma")...)
			send(t, hx, key(tc.object))
			assert.Equal(t, tc.want, sel(t, hx), "ma%c", tc.object)
		}
	})

	t.Run("mis selects the sentence", func(t *testing.T) {
		hx, _, _ := newHelix(t, "One. Two.", term.Coordinates{X: 1})
		send(t, hx, keys("mis")...)
		assert.Equal(t, "One.", sel(t, hx))
	})

	t.Run("a modified key cancels match mode", func(t *testing.T) {
		hx, _, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, key('m'))
		_, handled := hx.Handle(modKey(term.ModCtrl, 'g'))
		assert.False(t, handled)
		assert.True(t, hx.IsNormalMode())
	})
}

// TestRegisters pins the " prefix, including the black hole register.
func TestRegisters(t *testing.T) {
	t.Run("a named register round trips", func(t *testing.T) {
		hx, buf, _ := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("\"awy")...)
		send(t, hx, keys("gl")...)
		send(t, hx, keys("\"ap")...)
		assert.Equal(t, "foo barfoo ", buf.String())
	})

	t.Run("the register only applies to the next operator", func(t *testing.T) {
		hx, _, clip := newHelix(t, "foo bar", term.Coordinates{})
		send(t, hx, keys("\"awy")...)
		send(t, hx, keys("wy")...)
		data, err := clip.Paste(registerNameToID('a'))
		require.NoError(t, err)
		assert.Equal(t, "foo ", data.Text, "the second yank used the default register")
	})

	t.Run("an invalid register name is swallowed", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('"'))
		_, handled := hx.Handle(modKey(term.ModCtrl, 'g'))
		assert.True(t, handled)
		assert.True(t, hx.IsNormalMode())
	})

	t.Run("a count survives the register prefix", func(t *testing.T) {
		hx, buf, clip := newHelix(t, "ab", term.Coordinates{})
		require.NoError(t, clip.Copy(registerNameToID('a'),
			clipboard.Data{Text: "X", Metadata: text.StandardSelection}))
		send(t, hx, keys("3\"ap")...)
		assert.Equal(t, "aXXXb", buf.String())
	})

	t.Run("register names normalise", func(t *testing.T) {
		for _, tc := range []struct {
			in    rune
			want  rune
			valid bool
		}{
			{in: '"', want: '"', valid: true},
			{in: 'a', want: 'a', valid: true},
			// Uppercase names append to the lowercase register.
			{in: 'Z', want: 'z', valid: true},
			{in: '0', want: '0', valid: true},
			{in: '+', want: '+', valid: true},
			{in: '_', want: '_', valid: true},
			{in: '/', want: '/', valid: true},
			{in: '.', want: '.', valid: true},
			{in: '-', want: '-', valid: true},
			{in: '!', valid: false},
			{in: ' ', valid: false},
			{in: 0, valid: false},
		} {
			assert.Equal(t, tc.valid, validRegisterName(tc.in), "%q", tc.in)
			if tc.valid {
				assert.Equal(t, tc.want, normalizedRegisterName(tc.in), "%q", tc.in)
			}
		}
		assert.Equal(t, clipboard.DefaultRegisterID, registerNameToID(0))
		assert.Equal(t, clipboard.DefaultRegisterID, registerNameToID('"'))
	})

	t.Run("an aborted register prefix does not eat a zero key", func(t *testing.T) {
		hx, _, _ := newHelix(t, "abc", term.Coordinates{})
		send(t, hx, key('"'))
		_, handled := hx.Handle(term.Event{Type: term.EventKey})
		assert.True(t, handled, "the prefix consumes the key")
		assert.True(t, hx.IsNormalMode())
	})
}
