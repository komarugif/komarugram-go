// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"time"
)

// DownloadedFile is a file the user downloaded into a directory of theirs,
// which only they delete.
type DownloadedFile struct {
	ID int64
	// Key is the message the file is of, and Topic the forum topic it is
	// in: 0 when it is in none, or for a file downloaded before topics were
	// kept.
	Key        MessageKey
	Topic      int
	Name, Path string
	Size       int64
	Added      time.Time
	State      DownloadState
}

// DownloadState is what became of a downloaded file.
type DownloadState uint8

const (
	// DownloadPresent: the file is as it was written.
	DownloadPresent DownloadState = iota
	// DownloadMissing: the file was moved or deleted.
	DownloadMissing
	// DownloadChanged: the file is not the size, or of the time, it was
	// written with.
	DownloadChanged
)

// Downloader keeps the files the user downloads.
type Downloader interface {
	// Download writes m's file into dir, or finds it there already, telling
	// how much of it is written; it is the file's path.
	Download(ctx context.Context, m Message, dir string, progress func(done, total int64)) (string, error)
	// Downloads are the files downloaded, the newest first.
	Downloads(ctx context.Context) ([]DownloadedFile, error)
	// DeleteDownloads deletes the files of ids from the disk; their media
	// stay in the cloud.
	DeleteDownloads(ctx context.Context, ids []int64) error
}
