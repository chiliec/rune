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
	"context"

	"github.com/unstablebuild/rune-go-sdk/term"
)

// surroundPair returns the opening and closing delimiters Helix uses for
// ch. Unmatched characters surround with themselves, which is how
// helix-core/src/surround.rs treats quotes and other symmetric pairs.
func surroundPair(ch rune) (open, close rune) {
	switch ch {
	case '(', ')':
		return '(', ')'
	case '{', '}':
		return '{', '}'
	case '[', ']':
		return '[', ']'
	case '<', '>':
		return '<', '>'
	}
	return ch, ch
}

// bracketPairs is match_brackets::BRACKETS restricted to the delimiters
// the cursor primitives can match.
var bracketPairs = [...][2]rune{{'(', ')'}, {'{', '}'}, {'[', ']'}, {'<', '>'}}

// bracketAt classifies ch as a pair delimiter, reporting the index into
// bracketPairs and whether it is the opening side. idx is -1 when ch is
// not a bracket at all.
func bracketAt(ch rune) (idx int, open bool) {
	for i, pair := range bracketPairs {
		switch ch {
		case pair[0]:
			return i, true
		case pair[1]:
			return i, false
		}
	}
	return -1, false
}

// closestPair implements the m textobject. find_nth_closest_pairs_plain
// walks forward from the selection looking for a close bracket whose
// opener lies behind the caret, so nested pairs that open and shut on
// the way are stepped over.
func (h *helixHandlerImpl) closestPair() (open, closing rune, ok bool) {
	pos, _, hasSelection := h.cursor.SelectionRange()
	if !hasSelection {
		pos = h.cursor.CursorAtScroll()
	}
	var stack []int
	for {
		ch, inBounds := h.charAt(pos)
		if !inBounds {
			return 0, 0, false
		}
		idx, isOpen := bracketAt(ch)
		switch {
		case idx < 0:
		case isOpen:
			stack = append(stack, idx)
		case len(stack) > 0 && stack[len(stack)-1] == idx:
			stack = stack[:len(stack)-1]
		default:
			return bracketPairs[idx][0], bracketPairs[idx][1], true
		}
		next, more := h.nextPos(pos)
		if !more {
			return 0, 0, false
		}
		pos = next
	}
}

// handleSurroundKey consumes the delimiter keys of ms / mr / md.
func (h *helixHandlerImpl) handleSurroundKey(ch rune) (quit, handled bool) {
	op := h.surroundMode
	if ch == 0 {
		return false, true
	}
	if op == surroundReplace && h.surroundFrom == 0 {
		h.surroundFrom = ch
		return false, true
	}

	h.surroundMode = surroundNone
	from := h.surroundFrom
	h.surroundFrom = 0
	defer func() {
		h.setMode(normalMode)
		h.resetCount()
	}()

	switch op {
	case surroundAdd:
		return false, h.surroundAdd(ch)
	case surroundDelete:
		return false, h.surroundDelete(ch)
	case surroundReplace:
		return false, h.surroundReplace(from, ch)
	}
	return false, true
}

// surroundAdd implements ms<char>: wrap the selection in the pair and
// keep the delimiters selected.
func (h *helixHandlerImpl) surroundAdd(ch rune) bool {
	from, to, ok := h.cursor.SelectionRange()
	if !ok {
		return false
	}
	open, closing := surroundPair(ch)
	buf := h.less.Buffer()
	ctx := context.Background()
	// Insert the closing delimiter first so the opening insert cannot
	// shift the position it was computed from.
	buf.Edit(ctx, to, to, string(closing))
	buf.Edit(ctx, from, from, string(open))

	end := to
	if end.Y == from.Y {
		end.X += 2
	} else {
		end.X++
	}
	h.reselectRange(from, end)
	h.exitSelectMode()
	return true
}

// surroundDelete implements md<char>.
func (h *helixHandlerImpl) surroundDelete(ch rune) bool {
	from, to, ok := h.surroundBounds(surroundPair(ch))
	if !ok {
		return false
	}
	last := to
	if last.X == 0 {
		return false
	}
	last.X--
	buf := h.less.Buffer()
	buf.Delete(last, to)
	buf.Delete(from, term.Coordinates{Y: from.Y, X: from.X + 1})

	inner := from
	innerEnd := last
	if innerEnd.Y == from.Y {
		innerEnd.X -= 2
	} else {
		innerEnd.X--
	}
	if coordinatesBefore(innerEnd, inner) {
		h.cursor.MoveToScroll(inner)
		h.anchorHere()
		return true
	}
	h.setSelectionRange(inner, innerEnd)
	h.exitSelectMode()
	return true
}

// surroundReplace implements mr<from><to>.
func (h *helixHandlerImpl) surroundReplace(from, to rune) bool {
	start, end, ok := h.surroundBounds(surroundPair(from))
	if !ok {
		return false
	}
	openTo, closeTo := surroundPair(to)
	last := end
	if last.X == 0 {
		return false
	}
	last.X--
	ctx := context.Background()
	buf := h.less.Buffer()
	buf.Edit(ctx, last, end, string(closeTo))
	buf.Edit(ctx, start, term.Coordinates{Y: start.Y, X: start.X + 1}, string(openTo))
	h.reselectRange(start, end)
	h.exitSelectMode()
	return true
}

// surroundBounds locates the delimiter pair enclosing the caret without
// disturbing the visible selection.
func (h *helixHandlerImpl) surroundBounds(open, closing rune) (from, to term.Coordinates, ok bool) {
	origin := h.cursor.CursorAtScroll()
	anchor, hadAnchor := h.selectionAnchor()
	h.cursor.Unselect()
	h.explicitSel = false
	if open == closing {
		ok = h.cursor.SelectAQuote(open)
	} else {
		ok = h.cursor.SelectABlock(open, closing)
	}
	if ok {
		from, to, ok = h.cursor.SelectionRange()
	}
	h.cursor.Unselect()
	if hadAnchor {
		h.setSelectionRange(anchor, origin)
	} else {
		h.cursor.MoveToScroll(origin)
		h.anchorHere()
	}
	return from, to, ok
}
