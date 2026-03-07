package renderer

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/config"
)


// drawBlocksSnap renders visible command block decorations onto r.blocksLayer
// using a pre-copied snapshot of block data. No buffer lock is held during rendering.
// Pattern: snapshot — data is copied under RLock in DrawAll; this function is lock-free.
func (r *Renderer) drawBlocksSnap(snap *blockSnap) {
	cfg := r.cfg.Blocks

	blocks := snap.blocks // already includes active block copy (appended in DrawAll)
	if len(blocks) == 0 {
		return
	}

	rows := snap.rows
	sbLen := snap.sbLen
	viewOff := snap.viewOff
	rect := snap.paneRect

	cw := r.font.CellW
	ch := r.font.CellH

	borderColor := config.ParseHexColor(cfg.BorderColor)
	successColor := config.ParseHexColor(cfg.SuccessColor)
	failColor := config.ParseHexColor(cfg.FailColor)

	stripeW := cfg.BorderWidth
	if stripeW < 1 {
		stripeW = 1
	}

	// Optional background tint image — reuse the existing 1×1 blockTintImg.
	// Premultiply RGB by alpha: Ebitengine uses premultiplied alpha internally.
	// Passing R=255, G=255, B=255, A=25 without premultiplication produces full
	// white because the GPU blend treats RGB as already premultiplied.
	var hasBg bool
	var bgImg *ebiten.Image
	if cfg.BgColor != "" && cfg.BgAlpha > 0 {
		base := config.ParseHexColor(cfg.BgColor)
		a := cfg.BgAlpha
		tint := color.RGBA{
			R: uint8(float64(base.R) * a),
			G: uint8(float64(base.G) * a),
			B: uint8(float64(base.B) * a),
			A: uint8(a * 255),
		}
		r.blockTintImg.Fill(tint)
		bgImg = r.blockTintImg
		hasBg = true
	}

	mx, my := ebiten.CursorPosition()
	cursor := image.Pt(mx, my)

	for i, b := range blocks {
		// Only draw blocks where a command actually ran.
		if b.AbsCmdRow < 0 {
			continue
		}

		startAbs := b.AbsPromptRow
		endAbs := b.AbsEndRow
		if endAbs < 0 {
			endAbs = sbLen + snap.cursorRow
		}

		startDisplay := startAbs - sbLen + viewOff
		endDisplay := endAbs - sbLen + viewOff

		if endDisplay < 0 || startDisplay >= rows {
			continue
		}

		visStart := startDisplay
		if visStart < 0 {
			visStart = 0
		}
		visEnd := endDisplay
		if visEnd >= rows {
			visEnd = rows - 1
		}

		// Block box geometry — cell-aligned.
		// boxY0 = top of first visible cell, boxY1 = bottom of last visible cell.
		// No padding/gap offsets — borders and bg are confined to exact cell boundaries.
		pad := r.padding
		boxX0 := rect.Min.X + 1
		boxX1 := rect.Max.X - pad
		boxY0 := rect.Min.Y + visStart*ch + pad
		if boxY0 < rect.Min.Y {
			boxY0 = rect.Min.Y
		}
		boxY1 := rect.Min.Y + (visEnd+1)*ch + pad
		if boxY1 <= boxY0 {
			continue // block too thin to render
		}
		if boxY1 > rect.Max.Y {
			boxY1 = rect.Max.Y
		}

		exitColor := borderColor
		if b.ExitCode == 0 {
			exitColor = successColor
		} else if b.ExitCode > 0 {
			exitColor = failColor
		}

		// Optional background tint — covers the full block area; the left stripe
		// is drawn on top so it shows through correctly regardless.
		if hasBg {
			bgRect := image.Rect(boxX0, boxY0, boxX1, boxY1)
			op := &ebiten.DrawImageOptions{}
			op.GeoM.Scale(float64(bgRect.Dx()), float64(bgRect.Dy()))
			op.GeoM.Translate(float64(bgRect.Min.X), float64(bgRect.Min.Y))
			r.blocksLayer.DrawImage(bgImg, op)
		}

		// 4-sided border: left accent stripe + 1px top + 1px right + 1px bottom.
		// boxY1-1 is the last pixel of the last cell's descender gap — below the
		// baseline, typically blank for non-descender characters at normal sizes.
		r.blocksLayer.SubImage(image.Rect(boxX0, boxY0, boxX0+stripeW, boxY1)).(*ebiten.Image).Fill(exitColor)
		r.blocksLayer.SubImage(image.Rect(boxX0+stripeW, boxY0, boxX1, boxY0+1)).(*ebiten.Image).Fill(exitColor)
		r.blocksLayer.SubImage(image.Rect(boxX1-1, boxY0, boxX1, boxY1)).(*ebiten.Image).Fill(exitColor)
		r.blocksLayer.SubImage(image.Rect(boxX0, boxY1-1, boxX1, boxY1)).(*ebiten.Image).Fill(exitColor)

		// Badges and copy buttons only when the block top is visible.
		if startDisplay < 0 {
			continue
		}

		// Shift badges down one row when the block starts at the viewport top
		// and the pane is scrolled, so they don't overlap the scroll indicator pill.
		badgeRow := startDisplay
		if badgeRow == 0 && viewOff > 0 && visEnd > 0 {
			badgeRow = 1
		}
		badgeY := rect.Min.Y + badgeRow*ch + pad
		rightX := boxX1 - cw

		blockRect := image.Rect(boxX0, boxY0, boxX1, boxY1)
		hovered := cursor.In(blockRect)

		// Three copy buttons shown on hover.
		if hovered {
			btnW := 3 * cw
			gap := cw

			allX := rightX - btnW
			outX := allX - gap - btnW
			cmdX := outX - gap - btnW

			allRect := image.Rect(allX, boxY0, allX+btnW, boxY0+ch)
			outRect := image.Rect(outX, boxY0, outX+btnW, boxY0+ch)
			cmdRect := image.Rect(cmdX, boxY0, cmdX+btnW, boxY0+ch)

			var copyTarget CopyTarget
			switch {
			case cursor.In(allRect):
				copyTarget = CopyAll
			case cursor.In(outRect):
				copyTarget = CopyOutput
			case cursor.In(cmdRect):
				copyTarget = CopyCmdText
			}

			r.BlockHover = BlockHoverState{
				Active:     true,
				Buf:        snap.buf,
				AbsStart:   b.AbsPromptRow,
				AbsCmdRow:  b.AbsCmdRow,
				AbsEnd:     endAbs,
				CmdCol:     b.CmdCol,
				CopyTarget: copyTarget,
				CmdRect:    cmdRect,
				OutRect:    outRect,
				AllRect:    allRect,
				blockIdx:   i,
			}

			allColor := r.ui.Dim
			outColor := r.ui.Dim
			cmdColor := r.ui.Dim
			if copyTarget == CopyAll {
				allColor = r.ui.Accent
			}
			if copyTarget == CopyOutput {
				outColor = r.ui.Accent
			}
			if copyTarget == CopyCmdText {
				cmdColor = r.ui.Accent
			}
			r.font.DrawString(r.blocksLayer, "all", allX, badgeY, allColor)
			r.font.DrawString(r.blocksLayer, "out", outX, badgeY, outColor)
			r.font.DrawString(r.blocksLayer, "cmd", cmdX, badgeY, cmdColor)
			rightX = cmdX - gap
		}

		// Failure badge and duration — shown outside hover too.
		if b.ExitCode > 0 {
			badge := fmt.Sprintf("!%d", b.ExitCode)
			badgeW := len([]rune(badge)) * cw
			rightX -= badgeW
			r.font.DrawString(r.blocksLayer, badge, rightX, badgeY, failColor)
			rightX -= cw
		}

		if cfg.ShowDuration && b.Duration >= time.Second {
			dur := formatDuration(b.Duration)
			durW := len([]rune(dur)) * cw
			rightX -= durW
			r.font.DrawString(r.blocksLayer, dur, rightX, badgeY, r.ui.Dim)
		}
	}
}

// formatDuration formats a duration as a short human string (e.g. "2s", "1m30s").
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// StripPrompt removes the shell prompt prefix from a command line, returning
// only the text the user typed. Strips everything up to and including the last
// occurrence of common prompt terminators ($ % # >) followed by a space.
func StripPrompt(line string) string {
	for _, term := range []string{"$ ", "% ", "# ", "> "} {
		if idx := strings.LastIndex(line, term); idx >= 0 {
			return strings.TrimSpace(line[idx+len(term):])
		}
	}
	return strings.TrimSpace(line)
}
