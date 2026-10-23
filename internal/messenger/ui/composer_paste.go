// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/io/transfer"
	"gioui.org/layout"

	"komarugram/internal/messenger/model"
)

// updatePaste takes Ctrl+V before the field: files, then a picture, then
// text, as Telegram Desktop.
func (c *messageComposer) updatePaste(gtx layout.Context, d *messageDraft) {
	for {
		_, e, ok := c.shortcuts.next(gtx, &d.editor, "composer.paste")
		if !ok {
			break
		}
		if e.State == key.Press {
			types := []string{clipboard.TypeText}
			if c.permissions(c.chat).Any(model.SendAttachments) {
				types = []string{clipboard.TypeURIList, clipboard.TypePNG, clipboard.TypeText}
			}
			gtx.Execute(clipboard.ReadCmd{Tag: &c.paste, Types: types})
		}
	}
	for {
		ev, ok := gtx.Event(
			transfer.TargetFilter{Target: &c.paste, Type: clipboard.TypeURIList},
			transfer.TargetFilter{Target: &c.paste, Type: clipboard.TypePNG},
			transfer.TargetFilter{Target: &c.paste, Type: clipboard.TypeText},
		)
		if !ok {
			break
		}
		e, ok := ev.(transfer.DataEvent)
		if !ok {
			continue
		}
		r := e.Open()
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			continue
		}
		switch e.Type {
		case clipboard.TypeURIList:
			if paths := localPaths(string(content)); len(paths) > 0 {
				c.files.addPaths(c, paths, false)
			} else {
				gtx.Execute(clipboard.ReadCmd{Tag: &c.paste, Types: []string{clipboard.TypePNG, clipboard.TypeText}})
			}
		case clipboard.TypePNG:
			if path, err := c.savePasted(content); err == nil {
				c.files.addPaths(c, []string{path}, false)
			} else {
				d.err = err
			}
		case clipboard.TypeText:
			if d.editor.Insert(string(content)) > 0 {
				c.textChanged(d)
			}
		}
	}
}

// localPaths are the files of a text/uri-list, nil unless all are local.
func localPaths(list string) []string {
	var paths []string
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			return nil
		}
		path := u.Path
		if runtime.GOOS == "windows" {
			if u.Host != "" && u.Host != "localhost" {
				path = `\\` + u.Host + path
			} else {
				path = strings.TrimPrefix(path, "/")
			}
			path = filepath.FromSlash(path)
		} else if u.Host != "" && u.Host != "localhost" {
			if name, _ := os.Hostname(); u.Host != name {
				return nil
			}
		}
		paths = append(paths, path)
	}
	return paths
}

// savePasted writes a pasted picture to image.png in a directory of its own.
func (c *messageComposer) savePasted(png []byte) (string, error) {
	dir, err := os.MkdirTemp("", "komarugram-paste-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	c.pasted[path] = true
	return path, nil
}

// forgetPasted removes pasted pictures among paths, or all not uploading.
func (c *messageComposer) forgetPasted(paths []string) {
	for path := range c.pasted {
		if paths == nil && !c.uploading[path] || slices.Contains(paths, path) {
			delete(c.pasted, path)
			delete(c.uploading, path)
			os.RemoveAll(filepath.Dir(path))
		}
	}
}
