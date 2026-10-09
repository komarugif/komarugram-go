// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"runtime"
	"time"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"

	"komarugram/assets"
	"komarugram/internal/messenger/localization"
	"komarugram/pkg/deviceinfo"
)

const (
	// repository is the program's source.
	repository = "github.com/komarugif/komarugram-go"
	// community is the username of KomaruGram's chat in Telegram.
	community = "komarugram"
	// productName is the program's name, which no language translates.
	productName = "KomaruGram Go"
	// githubMark is GitHub's logo, which its terms keep out of the
	// repository: it is fetched when the section opens.
	githubMark = "https://github.githubassets.com/favicons/favicon.svg"
)

// maintainers are the GitHub users who keep the program on each system.
var maintainers = map[string]string{
	"linux":   "komarugif",
	"haiku":   "komarugif",
	"windows": "Augustwise",
	"darwin":  "aleksfolt",
}

// aboutView is the settings' About section: the program's logo and name,
// the system it runs on and the versions of its main dependencies, and
// links to its repository, to its maintainer on this system and to its
// community in Telegram.
type aboutView struct {
	logo                        paint.ImageOp
	logoLoaded                  bool
	repo, maintainer, community settingsItem
	height                      heightTransition

	// openCommunity opens the community's chat in the client; nil leaves
	// the row out. communityProblem is why it did not open.
	openCommunity    func()
	communityProblem string

	// The pictures fetched from GitHub: its mark, drawn as an icon in the
	// theme's color, and the maintainer's avatar.
	fetching   bool
	fetched    chan aboutPictures
	mark       *oksvg.SvgIcon
	markImage  paint.ImageOp
	markKey    markKey
	avatar     image.Image
	avatarOp   paint.ImageOp
	invalidate func()
	// fetch gets a URL's body; tests replace it.
	fetch func(ctx context.Context, url string) ([]byte, error)

	// mascot is Claude's, played where animate allows.
	mascot  *mascotView
	animate func() bool
}

type aboutPictures struct {
	mark   *oksvg.SvgIcon
	avatar image.Image
}

// markKey is what the mark was last drawn for.
type markKey struct {
	px    int
	color color.NRGBA
}

func newAboutView(invalidate func()) *aboutView {
	if invalidate == nil {
		invalidate = func() {}
	}
	v := &aboutView{fetched: make(chan aboutPictures, 1), invalidate: invalidate, fetch: fetchSmall}
	v.mascot = newMascotView(invalidate, func(ctx context.Context, url string) ([]byte, error) { return v.fetch(ctx, url) })
	return v
}

// maintainer is this system's maintainer, or "" where there is none.
func maintainer() string { return maintainers[runtime.GOOS] }

// open fetches what is missing of the pictures, once at a time.
func (v *aboutView) open() {
	v.mascot.open()
	if v.fetching || v.mark != nil && (v.avatar != nil || maintainer() == "") {
		return
	}
	v.fetching = true
	fetch, fetched, invalidate := v.fetch, v.fetched, v.invalidate
	user := maintainer()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var p aboutPictures
		if data, err := fetch(ctx, githubMark); err != nil {
			log.Printf("about: GitHub's mark: %v", err)
		} else if p.mark, err = oksvg.ReadIconStream(bytes.NewReader(data)); err != nil {
			log.Printf("about: GitHub's mark: %v", err)
		}
		if user != "" {
			if data, err := fetch(ctx, "https://github.com/"+user+".png?size=96"); err != nil {
				log.Printf("about: %s's avatar: %v", user, err)
			} else if p.avatar, _, err = image.Decode(bytes.NewReader(data)); err != nil {
				log.Printf("about: %s's avatar: %v", user, err)
			}
		}
		fetched <- p
		invalidate()
	}()
}

// fetchSmall gets url's body, of at most a megabyte. The tests replace
// it: they reach no server.
var fetchSmall = httpGetSmall

func httpGetSmall(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && len(data) > limit {
		err = errors.New("larger than a megabyte")
	}
	return data, err
}

// Update handles the links and takes the pictures that came.
func (v *aboutView) Update(gtx layout.Context) {
	v.mascot.Update(gtx)
	select {
	case p := <-v.fetched:
		v.fetching = false
		if p.mark != nil {
			v.mark = p.mark
		}
		if p.avatar != nil {
			v.avatar, v.avatarOp = p.avatar, paint.NewImageOp(p.avatar)
		}
	default:
	}
	if v.repo.click.Clicked(gtx) {
		openLater("https://" + repository)
	}
	if user := maintainer(); user != "" && v.maintainer.click.Clicked(gtx) {
		openLater("https://github.com/" + user)
	}
	if v.openCommunity != nil && v.community.click.Clicked(gtx) {
		v.communityProblem = ""
		v.openCommunity()
	}
}

// openLater opens target in the browser without holding up the frame.
func openLater(target string) {
	go func() {
		if err := openBrowser(target); err != nil {
			log.Printf("open %s: %v", target, err)
		}
	}()
}

func (v *aboutView) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
				return v.layoutCard(gtx, l)
			}, defaultCardPadding)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return card(gtx, func(gtx layout.Context) layout.Dimensions {
				rows := []layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return v.repo.layout(gtx, v.layoutMark, gtx.Dp(24), l.T("about.repository"), repository)
					}),
				}
				if user := maintainer(); user != "" {
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return v.maintainer.layout(gtx, v.layoutAvatar, gtx.Dp(24), l.T("about.maintainer_"+runtime.GOOS), "github.com/"+user)
					}))
				}
				if v.openCommunity != nil {
					subtitle := "t.me/" + community
					if v.communityProblem != "" {
						subtitle = v.communityProblem
					}
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return v.community.Layout(gtx, iconGroups, l.T("about.community"), subtitle)
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}, 6)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.mascot.Layout(gtx, l, v.animate == nil || v.animate())
		}),
	)
}

// layoutCard draws the logo and the name, then the system and the
// versions of the dependencies.
func (v *aboutView) layoutCard(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if !v.logoLoaded {
		v.logoLoaded = true
		if img, err := png.Decode(bytes.NewReader(assets.LogoRound)); err == nil {
			v.logo = paint.NewImageOp(img)
		}
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					px := gtx.Dp(64)
					gtx.Constraints = layout.Exact(image.Pt(px, px))
					return widget.Image{Src: v.logo, Fit: widget.Contain}.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: 16}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, productName, token.TypestyleHeadlineSmall, sc.Surface.OnColor, 1)
				}),
			)
		}),
		vspace(16),
		aboutRow(l.T("about.platform"), deviceinfo.Platform()),
	}
	for _, d := range assets.Dependencies() {
		rows = append(rows, vspace(6), aboutRow(d.Name, d.Version))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// aboutRow is a line of the card: what, then its value.
func aboutRow(name, value string) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		return layout.Flex{}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(110)
				gtx.Constraints.Max.X = max(gtx.Constraints.Max.X, gtx.Constraints.Min.X)
				return label(gtx, name, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, value, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
			}),
		)
	})
}

// layoutMark draws GitHub's mark in the color of the icons, or a link's
// icon until it has come.
func (v *aboutView) layoutMark(gtx layout.Context) layout.Dimensions {
	sc := scheme(gtx)
	px := gtx.Constraints.Max.X
	if v.mark == nil {
		return iconLink(gtx, sc.Primary.Color)
	}
	key := markKey{px: px, color: sc.Primary.Color.AsNRGBA()}
	if key != v.markKey {
		v.markKey, v.markImage = key, paint.NewImageOp(tintedMark(v.mark, px, key.color))
	}
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	return widget.Image{Src: v.markImage, Fit: widget.Contain}.Layout(gtx)
}

// tintedMark draws the shapes of mark in c, px wide and tall.
func tintedMark(mark *oksvg.SvgIcon, px int, c color.NRGBA) *image.NRGBA {
	shape := image.NewRGBA(image.Rect(0, 0, px, px))
	mark.SetTarget(0, 0, float64(px), float64(px))
	mark.Draw(rasterx.NewDasher(px, px, rasterx.NewScannerGV(px, px, shape, shape.Bounds())), 1)
	out := image.NewNRGBA(shape.Bounds())
	for i := 0; i < len(shape.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = c.R, c.G, c.B, uint8(uint16(shape.Pix[i+3])*uint16(c.A)/255)
	}
	return out
}

// layoutAvatar draws the maintainer's avatar in a circle, or a person's
// icon until it has come.
func (v *aboutView) layoutAvatar(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	if v.avatar == nil {
		return iconPersonal(gtx, scheme(gtx).Primary.Color)
	}
	defer avatarShape(gtx, size).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(size)
	return widget.Image{Src: v.avatarOp, Fit: widget.Cover}.Layout(gtx)
}
