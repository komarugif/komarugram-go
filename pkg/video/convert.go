// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"context"
)

// convertFormats are the demuxers a video to convert may be read by: containers of
// video, none that names other files, as HLS or concat do. The file is the user's
// choice, but may have come from anyone.
const convertFormats = "mov,matroska,webm,avi,ogg,asf,flv,mpegts,mpeg,gif"

// ProbeChosenFile is ProbeMedia of a file the user chose, in a format to convert from
// as well as those played.
func ProbeChosenFile(ctx context.Context, path, ffmpeg string) (Media, error) {
	return probeMedia(ctx, path, ffmpeg, convertFormats)
}

// ConvertToMP4 makes the video at in an MP4 at out, H.264 with AAC sound that plays
// as it comes, which every client shows as a video. It uses ffmpeg (the one found for
// "") and tells report how far it has come.
func ConvertToMP4(ctx context.Context, ffmpeg, in, out string, report func(GIFProgress)) error {
	if err := needH264(ffmpeg); err != nil {
		return err
	}
	media, err := probeMedia(ctx, in, ffmpeg, convertFormats)
	if err != nil {
		return err
	}
	return EncodeEdit(ctx, ffmpeg, in, out, EditPlan{Media: media, Formats: convertFormats}, report)
}
