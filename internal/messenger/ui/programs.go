// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"

	"gio-mw/token"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/pkg/miniapp"
	"komarugram/pkg/player"
	"komarugram/pkg/video"
)

// programSetting is a program the app hands work to, such as a player or
// the browser of Mini Apps: the one found on the system, or one the user
// points at. A picked file is kept only once it answered as the program it
// was picked as — any file can be picked, and running it would do whatever
// it does.
type programSetting struct {
	height heightTransition
	// title names the program in the settings.
	title string
	// custom is the path the user gave, "" for none; save keeps a new one.
	custom func() string
	save   func(string)
	// found returns the program found on the system and what it said it
	// is, or an error. It may take a second, and runs off the frame.
	found func(ctx context.Context) (path, about string, err error)
	// check makes sure the program at path is the right one and returns
	// what it is.
	check func(ctx context.Context, path string) (string, error)

	choose, reset surface
	picking       bool
	// pickErr is why the file picked last was turned down.
	pickErr error
	// detected is the program found, nil until known; customAbout is what
	// the program at customPath said.
	detected    *programAnswer
	customPath  string
	customAbout *programAnswer
	results     chan programAnswer
	ctx         context.Context
	invalidate  func()
}

// programAnswer is what a program said it is, or why it was turned down.
type programAnswer struct {
	path, about string
	err         error
	// found marks the answer about the program found; picked, about a file
	// the user picked; neither, about the path saved before.
	found, picked, cancelled bool
}

// refresh asks the found program and the saved one what they are, again: a
// program may have been installed, removed or updated since.
func (s *programSetting) refresh(ctx context.Context, invalidate func()) {
	s.ctx, s.invalidate = ctx, invalidate
	if s.results == nil {
		s.results = make(chan programAnswer, 8)
	}
	s.detected, s.customAbout, s.customPath = nil, nil, ""
	go func() {
		path, about, err := s.found(ctx)
		s.results <- programAnswer{path: path, about: about, err: err, found: true}
		invalidate()
	}()
	s.askCustom()
}

// askCustom asks the saved program what it is, once per path.
func (s *programSetting) askCustom() {
	path := s.custom()
	if path == s.customPath || s.results == nil {
		return
	}
	s.customPath, s.customAbout = path, nil
	if path == "" {
		return
	}
	go func() {
		about, err := s.check(s.ctx, path)
		s.results <- programAnswer{path: path, about: about, err: err}
		s.invalidate()
	}()
}

func (s *programSetting) Update(gtx layout.Context) {
	for done := false; !done; {
		select {
		case a := <-s.results:
			switch {
			case a.found:
				s.detected = &a
			case a.picked:
				s.picking = false
				if a.cancelled {
					break
				}
				s.pickErr = a.err
				if a.err == nil {
					s.save(a.path)
					s.customPath, s.customAbout = a.path, &a
				}
			case a.path == s.customPath:
				s.customAbout = &a
			}
		default:
			done = true
		}
	}
	s.askCustom()
	if s.choose.Clicked(gtx) && !s.picking && s.results != nil {
		s.picking, s.pickErr = true, nil
		go s.pick()
	}
	if s.reset.Clicked(gtx) {
		s.pickErr = nil
		s.save("")
		gtx.Execute(op.InvalidateCmd{})
	}
}

// pick lets the user choose a file and checks it.
func (s *programSetting) pick() {
	defer s.invalidate()
	choice := chooseAttachment(s.ctx, false)
	if choice.err == nil && choice.path == "" {
		s.results <- programAnswer{picked: true, cancelled: true}
		return
	}
	answer := programAnswer{path: choice.path, err: choice.err, picked: true}
	if answer.err == nil {
		answer.about, answer.err = s.check(s.ctx, choice.path)
	}
	s.results <- answer
}

func (s *programSetting) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	custom := s.custom()
	var rows []layout.FlexChild
	add := func(text string, style token.Typestyle, failed bool) {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			color := sc.SurfaceVariant.OnColor
			if failed {
				color = sc.Error.Color
			}
			return label(gtx, text, style, color, 3)
		}))
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, s.title, token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
	}), vspace(8))
	switch {
	case custom != "":
		add(l.T("program.custom")+": "+custom, token.TypestyleBodyMedium, false)
		if a := s.customAbout; a != nil && a.path == custom {
			if a.err != nil {
				add(programErrorText(a.err, s.title, l), token.TypestyleBodyMedium, true)
			} else {
				add(a.about, token.TypestyleBodyMedium, false)
			}
		}
	case s.detected == nil:
		// Still asking; the line appears when the answer comes.
	case s.detected.path == "":
		add(l.T("program.not_found"), token.TypestyleBodyMedium, false)
	default:
		add(l.T("program.found")+": "+s.detected.path, token.TypestyleBodyMedium, false)
		if s.detected.err != nil {
			add(programErrorText(s.detected.err, s.title, l), token.TypestyleBodyMedium, true)
		} else if s.detected.about != "" {
			add(s.detected.about, token.TypestyleBodyMedium, false)
		}
	}
	if s.pickErr != nil {
		rows = append(rows, vspace(4))
		add(l.T("program.rejected")+": "+programErrorText(s.pickErr, s.title, l), token.TypestyleBodyMedium, true)
	}
	rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		buttons := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if s.picking {
				gtx = gtx.Disabled()
			}
			return textButton(gtx, &s.choose, l.T("program.choose"))
		})}
		if custom != "" {
			buttons = append(buttons, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return textButton(gtx, &s.reset, l.T("program.reset"))
			}))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, buttons...)
	}))
	return s.height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	}, defaultCardPadding)
}

// programErrorText says why a program was turned down.
func programErrorText(err error, name string, l localization.Catalog) string {
	var version *player.VersionError
	switch {
	case errors.Is(err, video.ErrNotExecutable), errors.Is(err, player.ErrNotExecutable), errors.Is(err, miniapp.ErrNotExecutable):
		return l.T("program.not_executable")
	case errors.Is(err, video.ErrWrongProgram), errors.Is(err, player.ErrWrongProgram):
		return l.Format("program.wrong", map[string]string{"program": name})
	case errors.Is(err, miniapp.ErrNotChromium):
		return l.T("program.not_chromium")
	case errors.Is(err, player.ErrSnap):
		return l.T("program.snap")
	case errors.Is(err, player.ErrUnsupportedSystem):
		return l.T("program.unsupported_system")
	case errors.As(err, &version):
		return l.Format("program.old_version", map[string]string{"version": version.Version, "want": version.Want})
	}
	return l.T("program.failed") + ": " + mediaErrorText(err)
}
