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
	"strings"
	"unicode"

	"github.com/unstablebuild/rune-go-sdk/clipboard"
	"github.com/unstablebuild/rune-go-sdk/term"
	"unstable.build/rune/internal/text"
)

// reselectRange restores an anchor/head selection spanning the
// half-open range [from, to). Helix operators keep their selection, but
// the cursor primitives clear it, so it has to be rebuilt afterwards.
func (h *helixHandlerImpl) reselectRange(from, to term.Coordinates) {
	last := to
	if last.X > 0 {
		last.X--
	}
	h.setSelectionRange(from, last)
}

func (h *helixHandlerImpl) reselectLines(startY, endY int) {
	h.cursor.Unselect()
	h.explicitSel = false
	h.cursor.MoveToScroll(term.Coordinates{Y: startY})
	h.cursor.SelectLine()
	if endY > startY {
		h.cursor.MoveDownLines(endY - startY)
	}
}

// keepSelection runs an in-place operator that clears the selection as
// a side effect and puts an equivalent selection back.
func (h *helixHandlerImpl) keepSelection(fn func() bool) bool {
	from, to, ok := h.cursor.SelectionRange()
	if !ok {
		return fn()
	}
	mode, _ := h.cursor.SelectionMode()
	changed := fn()
	if mode == text.LineSelection {
		h.reselectLines(from.Y, to.Y)
	} else {
		h.reselectRange(from, to)
	}
	return changed
}

// keepLineSelection runs an operator that rewrites the indentation of
// the selected lines, so only the line span can be restored.
func (h *helixHandlerImpl) keepLineSelection(fn func()) bool {
	from, to, ok := h.cursor.SelectionRange()
	if !ok {
		fn()
		return true
	}
	fn()
	h.reselectLines(from.Y, to.Y)
	return true
}

func (h *helixHandlerImpl) shiftSelection(right bool) bool {
	return h.keepLineSelection(func() {
		for range h.motionCount() {
			if right {
				h.cursor.ShiftSelectionRight(h.config.indentRune, h.config.indentTabspaces)
			} else {
				h.cursor.ShiftSelectionLeft(h.config.indentRune, h.config.indentTabspaces)
			}
		}
	})
}

func (h *helixHandlerImpl) formatSelection() bool {
	return h.keepLineSelection(func() {
		h.cursor.ReindentSelection(h.config.indentRune, h.config.indentTabspaces)
	})
}

func (h *helixHandlerImpl) toggleComments() bool {
	return h.keepLineSelection(func() { h.cursor.ToggleLineComment() })
}

func (h *helixHandlerImpl) yankSelection() bool {
	mode, ok := h.cursor.SelectionMode()
	if !ok {
		return false
	}
	data := clipboard.Data{Text: h.selectionText(), Metadata: mode}
	reg := h.consumeActiveRegister()
	if _, err := h.cursor.CopySelectionNoUnselect(
		registerNameToID(reg), h.config.clipboard); err != nil {
		h.logError(err)
		return false
	}
	if reg != unnamedRegister {
		if err := h.writeRegister(unnamedRegister, data); err != nil {
			h.logError(err)
		}
	}
	if reg != lastYankRegister {
		if err := h.writeRegister(lastYankRegister, data); err != nil {
			h.logError(err)
		}
	}
	return true
}

// copySelectionForDelete fills the registers Helix writes on a delete
// and reports whether the cursor's own copy-on-delete must be
// suppressed, which is what the black hole register asks for.
func (h *helixHandlerImpl) copySelectionForDelete() bool {
	reg := h.consumeActiveRegister()
	if reg == blackHoleRegister {
		return true
	}
	mode, _ := h.cursor.SelectionMode()
	data := clipboard.Data{Text: h.selectionText(), Metadata: mode}
	if reg != unnamedRegister {
		if err := h.writeRegister(reg, data); err != nil {
			h.logError(err)
		}
	}
	if err := h.writeRegister(unnamedRegister, data); err != nil {
		h.logError(err)
	}
	if mode != text.LineSelection {
		if err := h.writeRegister('-', data); err != nil {
			h.logError(err)
		}
	}
	return false
}

// deleteSelection removes the selection. Helix's Alt-d/Alt-c variants
// pass yank=false so the registers and the system clipboard are left
// untouched.
func (h *helixHandlerImpl) deleteSelection(yank bool) bool {
	if _, ok := h.cursor.SelectionMode(); !ok {
		return false
	}
	if yank {
		h.suppressCopyDelete = h.copySelectionForDelete()
	} else {
		h.selectedRegister = 0
		h.suppressCopyDelete = true
	}
	ok := h.cursor.DeleteSelection()
	h.suppressCopyDelete = false
	h.explicitSel = false
	return ok
}

// changeSelection implements c and Alt-c. A selection that covers whole
// lines is replaced by a fresh blank line rather than collapsing the
// surrounding ones, which is delete_selection_impl's only_whole_lines
// branch.
func (h *helixHandlerImpl) changeSelection(yank bool) bool {
	mode, ok := h.cursor.SelectionMode()
	linewise := ok && mode == text.LineSelection
	deleted := h.deleteSelection(yank)
	if linewise {
		h.openLine(true)
		return deleted
	}
	h.setInsertMode()
	return deleted
}

// pasteClipboard inserts the register contents next to the selection.
// Unlike vi's visual-mode paste, Helix never replaces the selection:
// that is what R does. paste_impl anchors a characterwise paste at
// range.from()/range.to() and a linewise paste at the surrounding line
// boundaries.
func (h *helixHandlerImpl) pasteClipboard(after bool) bool {
	data, err := h.readRegister(h.consumeActiveRegister())
	if err != nil {
		h.logError(err)
		return false
	}
	if data.Text == "" {
		return false
	}
	mode, _ := data.Metadata.(text.SelectMode)

	from, to, hasSelection := h.cursor.SelectionRange()
	h.cursor.Unselect()
	h.explicitSel = false
	if hasSelection {
		target := from
		if after {
			target = to
			if target.X > 0 {
				target.X--
			}
		}
		h.cursor.MoveToScroll(target)
	}
	// paste_impl repeats the register contents, not the insertion, so a
	// counted paste lands as one contiguous run.
	h.cursor.Paste(strings.Repeat(data.Text, h.motionCount()), mode, after)
	h.anchorHere()
	return true
}

// replaceWithYanked implements Helix's R: swap the selection for the
// register contents.
func (h *helixHandlerImpl) replaceWithYanked() bool {
	data, err := h.readRegister(h.consumeActiveRegister())
	if err != nil {
		h.logError(err)
		return false
	}
	if _, ok := h.cursor.SelectionMode(); !ok {
		return false
	}
	mode, _ := data.Metadata.(text.SelectMode)
	h.suppressCopyDelete = true
	h.cursor.Paste(data.Text, mode, false)
	h.suppressCopyDelete = false
	h.explicitSel = false
	h.anchorHere()
	return true
}

// replaceSelection implements Helix's r<char>: every character in the
// selection becomes ch and the selection survives.
func (h *helixHandlerImpl) replaceSelection(ch rune) bool {
	from, to, ok := h.cursor.SelectionRange()
	if !ok {
		return false
	}
	buf := h.less.Buffer()
	replacement := string(ch)
	replaced := false
	ctx := context.Background()
	for y := from.Y; y <= to.Y && y < buf.Rows(); y++ {
		columns := buf.Columns(y)
		startX := 0
		if y == from.Y {
			startX = from.X
		}
		endX := columns
		if y == to.Y {
			endX = min(to.X, columns)
		}
		for x := startX; x < endX; x++ {
			buf.Edit(ctx, term.Coordinates{X: x, Y: y},
				term.Coordinates{X: x + 1, Y: y}, replacement)
			replaced = true
		}
	}
	if !replaced {
		return false
	}
	if ch == '\n' {
		// Newlines change the shape of the buffer, so the original
		// range no longer describes anything meaningful.
		h.anchorHere()
		return true
	}
	h.reselectRange(from, to)
	return true
}

// joinSelection collapses every line the selection touches onto the
// first of them. selectSpace implements A-J, which leaves the inserted
// separator selected.
func (h *helixHandlerImpl) joinSelection(selectSpace bool) bool {
	from, to, ok := h.cursor.SelectionRange()
	lines := 1
	if ok {
		lines = max(1, to.Y-from.Y)
	}
	h.cursor.Unselect()
	h.explicitSel = false
	if ok {
		h.cursor.MoveToScroll(from)
	}
	joined := false
	var lastJoin term.Coordinates
	for range lines {
		if !h.cursor.Join() {
			break
		}
		lastJoin = h.cursor.CursorAtScroll()
		joined = true
	}
	if joined && selectSpace {
		h.setSelectionRange(lastJoin, lastJoin)
		return true
	}
	h.anchorHere()
	return joined
}

// extendLineBelow implements x: the first press snaps the selection to
// whole lines, repeats grow it downwards by count lines.
func (h *helixHandlerImpl) extendLineBelow() bool {
	count := h.motionCount()
	mode, ok := h.cursor.SelectionMode()
	if ok && mode == text.LineSelection && !h.explicitSel {
		return h.cursor.MoveDownLines(count)
	}
	h.cursor.Unselect()
	h.explicitSel = false
	selected := h.cursor.SelectLine()
	if count > 1 {
		h.cursor.MoveDownLines(count - 1)
	}
	return selected
}

// extendToLineBounds implements X: snap whatever is selected out to
// whole lines without moving further.
func (h *helixHandlerImpl) extendToLineBounds() bool {
	if _, ok := h.cursor.SelectionMode(); !ok {
		return false
	}
	h.explicitSel = false
	return h.cursor.SelectLine()
}

// shrinkToLineBounds implements A-x: drop the partially covered first
// and last lines. Selections inside a single line are left alone, which
// is what shrink_to_line_bounds does.
func (h *helixHandlerImpl) shrinkToLineBounds() bool {
	from, to, ok := h.cursor.SelectionRange()
	if !ok {
		return false
	}
	mode, _ := h.cursor.SelectionMode()
	lastY := to.Y
	if to.X == 0 && lastY > from.Y {
		lastY--
	}
	if from.Y == lastY {
		return false
	}
	startY, endY := from.Y, lastY
	if from.X > 0 {
		startY++
	}
	if mode != text.LineSelection && to.X < h.less.Buffer().Columns(lastY) {
		endY--
	}
	if startY > endY {
		return false
	}
	h.reselectLines(startY, endY)
	return true
}

// trimSelection implements _: shrink the selection past the whitespace
// at either end. An all-whitespace selection collapses onto the caret,
// which is what trim_selections falls back to.
func (h *helixHandlerImpl) trimSelection() bool {
	from, to, ok := h.cursor.SelectionRange()
	if !ok {
		return false
	}
	last, ok := h.prevPos(to)
	if !ok {
		return false
	}
	// A linewise selection owns the separator of its last line, which
	// the cell range reports as the position just past it.
	if mode, _ := h.cursor.SelectionMode(); mode == text.LineSelection {
		last = to
	}
	start, startOK := h.skipSpace(from, last, h.nextPos)
	end, endOK := h.skipSpace(last, from, h.prevPos)
	if !startOK || !endOK || coordinatesBefore(end, start) {
		h.anchorHere()
		return true
	}
	// A line selection carries its separator implicitly, so there is
	// always a newline to shave off even when the range itself starts
	// and ends on non-blank cells.
	mode, _ := h.cursor.SelectionMode()
	if start == from && end == last && mode != text.LineSelection {
		return false
	}
	if h.selectionBackward() {
		h.setSelectionRange(end, start)
	} else {
		h.setSelectionRange(start, end)
	}
	return true
}

// skipSpace walks from pos towards limit while the character under it is
// whitespace, reporting false when the whole span is blank.
func (h *helixHandlerImpl) skipSpace(
	pos, limit term.Coordinates, step func(term.Coordinates) (term.Coordinates, bool),
) (term.Coordinates, bool) {
	for {
		ch, ok := h.charAt(pos)
		if !ok {
			return pos, false
		}
		if !unicode.IsSpace(ch) {
			return pos, true
		}
		if pos == limit {
			return pos, false
		}
		next, ok := step(pos)
		if !ok {
			return pos, false
		}
		pos = next
	}
}

// selectAll implements %.
func (h *helixHandlerImpl) selectAll() bool {
	buf := h.less.Buffer()
	rows := buf.Rows()
	if rows == 0 {
		return false
	}
	lastY := rows - 1
	to := term.Coordinates{X: buf.Columns(lastY), Y: lastY}
	if !h.cursor.SelectRange(term.Coordinates{}, to) {
		return false
	}
	h.explicitSel = true
	return true
}

// insertBeforeSelection implements i and is also the entry point the
// Helix wrapper uses to replay the last insert for `.`.
func (h *helixHandlerImpl) insertBeforeSelection() {
	if from, _, ok := h.cursor.SelectionRange(); ok {
		h.cursor.Unselect()
		h.explicitSel = false
		h.cursor.MoveToScroll(from)
	}
	h.setInsertMode()
	h.insertKeepsCaret = true
}

// insertAfterSelection implements a: the caret lands one cell past the
// selection, which is where Helix appends.
func (h *helixHandlerImpl) insertAfterSelection() {
	if _, to, ok := h.cursor.SelectionRange(); ok {
		h.cursor.Unselect()
		h.explicitSel = false
		h.cursor.MoveToScroll(to)
	}
	h.setInsertMode()
}

func (h *helixHandlerImpl) insertAtLineStart() {
	h.cursor.Unselect()
	h.explicitSel = false
	h.cursor.MoveStartLineNonBlank()
	h.setInsertMode()
}

func (h *helixHandlerImpl) insertAtLineEnd() {
	h.cursor.Unselect()
	h.explicitSel = false
	h.cursor.MoveEndLine()
	h.setInsertMode()
}

func (h *helixHandlerImpl) openLine(above bool) {
	count := h.motionCount()
	h.cursor.Unselect()
	h.explicitSel = false
	h.setInsertMode()
	for i := range count {
		if above && i == 0 {
			h.cursor.InsertLineAbove(h.config.indentRune, h.config.indentTabspaces)
			continue
		}
		h.cursor.InsertLineBelow(h.config.indentRune, h.config.indentTabspaces)
	}
}

// addNewline implements [<space> and ]<space>: a blank line is added
// without leaving normal mode or moving the caret.
func (h *helixHandlerImpl) addNewline(below bool) bool {
	origin := h.cursor.CursorAtScroll()
	buf := h.less.Buffer()
	at := term.Coordinates{Y: origin.Y}
	if below {
		at = term.Coordinates{Y: origin.Y, X: buf.Columns(origin.Y)}
	}
	ctx := context.Background()
	for range h.motionCount() {
		buf.Edit(ctx, at, at, "\n")
	}
	target := origin
	if !below {
		target.Y += h.motionCount()
	}
	h.cursor.MoveToScroll(target)
	h.anchorHere()
	return true
}

// selectTextObject implements the mi/ma pairs.
func (h *helixHandlerImpl) selectTextObject(ch rune) bool {
	around := h.matchAround
	h.matchPending = false
	h.matchAround = false

	var ok bool
	switch ch {
	case 'm':
		open, closing, found := h.closestPair()
		if !found {
			return false
		}
		if around {
			ok = h.cursor.SelectABlock(open, closing)
		} else {
			ok = h.cursor.SelectInnerBlock(open, closing)
		}
	case 'w':
		if around {
			ok = h.cursor.SelectAWords(h.motionCount())
		} else {
			ok = h.cursor.SelectInnerWords(h.motionCount())
		}
	case 'W':
		if around {
			ok = h.cursor.SelectAWordGroups(h.motionCount())
		} else {
			ok = h.cursor.SelectInnerWordGroups(h.motionCount())
		}
	case 's':
		if around {
			ok = h.cursor.SelectASentence()
		} else {
			ok = h.cursor.SelectInnerSentence()
		}
	case 'p':
		if around {
			ok = h.cursor.SelectAParagraph()
		} else {
			ok = h.cursor.SelectInnerParagraph()
		}
	case '"', '\'', '`':
		if around {
			ok = h.cursor.SelectAQuote(ch)
		} else {
			ok = h.cursor.SelectInnerQuote(ch)
		}
	case '(', ')', 'b':
		if around {
			ok = h.cursor.SelectABlock('(', ')')
		} else {
			ok = h.cursor.SelectInnerBlock('(', ')')
		}
	case '{', '}', 'B':
		if around {
			ok = h.cursor.SelectABlock('{', '}')
		} else {
			ok = h.cursor.SelectInnerBlock('{', '}')
		}
	case '[', ']':
		if around {
			ok = h.cursor.SelectABlock('[', ']')
		} else {
			ok = h.cursor.SelectInnerBlock('[', ']')
		}
	case '<', '>', 't':
		if around {
			ok = h.cursor.SelectABlock('<', '>')
		} else {
			ok = h.cursor.SelectInnerBlock('<', '>')
		}
	default:
		return false
	}
	if ok {
		h.explicitSel = true
	}
	return ok
}
