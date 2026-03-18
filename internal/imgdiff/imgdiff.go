package imgdiff

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

// Compare checks whether two PNG images are similar within tolerance.
// If they differ beyond the thresholds, it writes a diff image to diffPath
// (when non-empty) and returns an error describing the difference.
// colorTol is the max per-channel RGBA delta before a pixel counts as different.
// pixelTol is the max fraction of pixels that may differ before failing.
func Compare(golden, actual []byte, diffPath string, colorTol uint8, pixelTol float64) error {
	goldenImg, err := png.Decode(bytes.NewReader(golden))
	if err != nil {
		return fmt.Errorf("decode golden: %w", err)
	}
	actualImg, err := png.Decode(bytes.NewReader(actual))
	if err != nil {
		return fmt.Errorf("decode actual: %w", err)
	}

	gb := goldenImg.Bounds()
	ab := actualImg.Bounds()
	if gb.Dx() != ab.Dx() || gb.Dy() != ab.Dy() {
		return fmt.Errorf("dimensions differ: golden %dx%d, actual %dx%d", gb.Dx(), gb.Dy(), ab.Dx(), ab.Dy())
	}

	totalPixels := gb.Dx() * gb.Dy()
	diffCount := 0
	diffImg := image.NewRGBA(gb)

	for y := gb.Min.Y; y < gb.Max.Y; y++ {
		for x := gb.Min.X; x < gb.Max.X; x++ {
			gr, gg, gbl, ga := goldenImg.At(x, y).RGBA()
			ar, ag, abl, aa := actualImg.At(x, y).RGBA()

			if channelDiff(gr, ar) > colorTol ||
				channelDiff(gg, ag) > colorTol ||
				channelDiff(gbl, abl) > colorTol ||
				channelDiff(ga, aa) > colorTol {
				diffCount++
				diffImg.Set(x, y, color.RGBA{R: 255, A: 255})
			} else {
				diffImg.Set(x, y, color.RGBA{
					R: uint8(gr >> 8 * 77 / 255),
					G: uint8(gg >> 8 * 77 / 255),
					B: uint8(gbl >> 8 * 77 / 255),
					A: 255,
				})
			}
		}
	}

	ratio := float64(diffCount) / float64(totalPixels)
	if ratio > pixelTol {
		if diffPath != "" {
			var diffBuf bytes.Buffer
			if err := png.Encode(&diffBuf, diffImg); err == nil {
				_ = os.WriteFile(diffPath, diffBuf.Bytes(), 0o644)
			}
		}
		return fmt.Errorf("%.2f%% pixels differ (threshold %.2f%%)", ratio*100, pixelTol*100)
	}
	return nil
}

func channelDiff(a, b uint32) uint8 {
	a8, b8 := uint8(a>>8), uint8(b>>8)
	if a8 > b8 {
		return a8 - b8
	}
	return b8 - a8
}
