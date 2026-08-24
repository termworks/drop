//go:build js || android

package gui

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// label is text at one of a few sizes, so the page has a scale rather than a pile of numbers.
func (a *App) label(text string, size unit.Sp, weight font.Weight, c color.NRGBA) material.LabelStyle {
	l := material.Label(a.theme, size, text)
	l.Color = c
	l.Font.Weight = weight
	return l
}

func (a *App) title(text string) material.LabelStyle {
	return a.label(text, sizeTitle, font.Bold, ink)
}

func (a *App) body(text string) material.LabelStyle {
	return a.label(text, sizeBody, font.Normal, ink)
}

func (a *App) small(text string, c color.NRGBA) material.LabelStyle {
	return a.label(text, sizeSmall, font.Normal, c)
}

func (a *App) tiny(text string, c color.NRGBA) material.LabelStyle {
	return a.label(text, sizeTiny, font.Normal, c)
}

// button is the one button shape: filled for the thing to do, outlined for everything else.
func (a *App) button(gtx layout.Context, click *widget.Clickable, text string, primary bool) layout.Dimensions {
	return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
		body := func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Dp(touch)

			return layout.Inset{Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					colour := violetOn
					if primary {
						colour = onDark
					}
					return a.label(text, sizeBody, font.Bold, colour).Layout(gtx)
				})
			})
		}

		if primary {
			return filled(gtx, round, body)
		}
		return tinted(gtx, wash, round, body)
	})
}

// dot is the small mark that says whether a device is paired.
func dot(gtx layout.Context, c color.NRGBA) layout.Dimensions {
	size := gtx.Dp(unit.Dp(8))
	rounded(gtx, image.Rect(0, 0, size, size), pill, c)

	return layout.Dimensions{Size: image.Pt(size, size)}
}

// row is one thing in a list: a card with a title, a tag, and a line saying what it is for.
//
// Two lines rather than one, because what is worth knowing about a device does not fit on one — and
// putting it in a second column instead makes the eye read across where it wants to read down.
func (a *App) row(gtx layout.Context, click *widget.Clickable, lead layout.Widget, title, tag, about string, tagColour color.NRGBA) layout.Dimensions {
	return layout.Inset{Bottom: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
			return card(gtx, round, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X

				return layout.UniformInset(pad).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if lead == nil {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: gap}.Layout(gtx, lead)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(a.label(title, sizeStrong, font.Bold, ink).Layout),
								layout.Rigid(layout.Spacer{Height: tight}.Layout),
								layout.Rigid(a.small(about, dim).Layout),
							)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if tag == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return tinted(gtx, wash, pill, func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{
										Top: tight, Bottom: tight, Left: gap, Right: gap,
									}.Layout(gtx, a.tiny(tag, tagColour).Layout)
								})
							})
						}),
					)
				})
			})
		})
	})
}

// note is what fills a pane when there is nothing to list.
func (a *App) note(gtx layout.Context, said string, c color.NRGBA) layout.Dimensions {
	return layout.Inset{Top: pad, Left: pad, Right: pad}.Layout(gtx, a.small(said, c).Layout)
}
