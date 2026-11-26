package server

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	drawpkg "image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func validateBrandingImage(source brandingImage) error {
	if source.contentType == "image/svg+xml" {
		content := bytes.TrimSpace(source.data)
		if !bytes.Contains(content, []byte("<svg")) {
			return fmt.Errorf("SVG image has no root element")
		}
		return nil
	}
	if source.contentType == "image/x-icon" || source.contentType == "image/vnd.microsoft.icon" {
		if len(source.data) < 6 || source.data[0] != 0 || source.data[1] != 0 || source.data[2] != 1 || source.data[3] != 0 {
			return fmt.Errorf("invalid icon image")
		}
		return nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(source.data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 10000 || config.Height > 10000 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return fmt.Errorf("invalid or oversized image")
	}
	wantFormat := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp"}[source.contentType]
	if format != wantFormat {
		return fmt.Errorf("image content does not match its declared type")
	}
	return nil
}

func resizeBrandingImage(source brandingImage, width, height int, cover bool) brandingImage {
	if source.contentType == "image/svg+xml" || width <= 0 || height <= 0 {
		return source
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(source.data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 10000 || config.Height > 10000 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return source
	}
	decoded, _, err := image.Decode(bytes.NewReader(source.data))
	if err != nil {
		return source
	}
	bound := decoded.Bounds()
	sourceWidth, sourceHeight := bound.Dx(), bound.Dy()
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return source
	}
	var targetWidth, targetHeight int
	if cover {
		if sourceWidth*height > sourceHeight*width {
			targetHeight = height
			targetWidth = sourceWidth * height / sourceHeight
		} else {
			targetWidth = width
			targetHeight = sourceHeight * width / sourceWidth
		}
	} else if sourceWidth*height > sourceHeight*width {
		targetWidth = width
		targetHeight = sourceHeight * width / sourceWidth
	} else {
		targetHeight = height
		targetWidth = sourceWidth * height / sourceHeight
	}
	if targetWidth <= 0 || targetHeight <= 0 {
		return source
	}
	resized := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), decoded, bound, drawpkg.Over, nil)
	canvas := resized
	if targetWidth != width || targetHeight != height {
		canvas = image.NewRGBA(image.Rect(0, 0, width, height))
		if !cover {
			drawpkg.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.Transparent}, image.Point{}, drawpkg.Src)
		}
		x, y := (width-targetWidth)/2, (height-targetHeight)/2
		drawpkg.Draw(canvas, image.Rect(x, y, x+targetWidth, y+targetHeight), resized, image.Point{}, drawpkg.Over)
	}
	var output bytes.Buffer
	if png.Encode(&output, canvas) != nil {
		return source
	}
	return brandingImage{data: output.Bytes(), contentType: "image/png", fetchedAt: source.fetchedAt}
}

func resizeBrandingWidth(source brandingImage, width int) brandingImage {
	if source.contentType == "image/svg+xml" || width <= 0 {
		return source
	}
	decoded, _, err := image.Decode(bytes.NewReader(source.data))
	if err != nil {
		return source
	}
	bound := decoded.Bounds()
	if bound.Dx() <= 0 || bound.Dy() <= 0 {
		return source
	}
	height := bound.Dy() * width / bound.Dx()
	if height <= 0 {
		return source
	}
	return resizeBrandingImage(source, width, height, false)
}
