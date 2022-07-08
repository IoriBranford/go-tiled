/*
Copyright (c) 2017 Lauris Bukšis-Haberkorns <lauris@nix.lv>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package render

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"

	"image/jpeg"

	"image/gif"

	"github.com/disintegration/imaging"
	"github.com/lafriks/go-tiled"
)

var (
	// ErrUnsupportedOrientation represents an error in the unsupported orientation for rendering.
	ErrUnsupportedOrientation = errors.New("tiled/render: unsupported orientation")
	// ErrUnsupportedRenderOrder represents an error in the unsupported order for rendering.
	ErrUnsupportedRenderOrder = errors.New("tiled/render: unsupported render order")
)

// RendererEngine is the interface implemented by objects that provide rendering engine for Tiled maps.
type RendererEngine interface {
	Init(m *tiled.Map)
	GetFinalImageSize() image.Rectangle
	RotateTileImage(tile *tiled.LayerTile, img image.Image) image.Image
	GetTilePosition(x, y int) image.Rectangle
}

// Renderer represents an rendering engine.
type Renderer struct {
	m         *tiled.Map
	Result    *image.NRGBA // The image result after rendering using the Render functions.
	TileCache map[uint32]image.Image
	Engine    RendererEngine
}

// NewRenderer creates new rendering engine instance.
func NewRenderer(m *tiled.Map) (*Renderer, error) {
	r := &Renderer{m: m, TileCache: make(map[uint32]image.Image)}
	if r.m.Orientation == "orthogonal" {
		r.Engine = &OrthogonalRendererEngine{}
	} else {
		return nil, ErrUnsupportedOrientation
	}

	r.Engine.Init(r.m)
	r.Clear()

	return r, nil
}

func (r *Renderer) getTileImage(tile *tiled.LayerTile) (image.Image, error) {
	timg, ok := r.TileCache[tile.Tileset.FirstGID+tile.ID]
	if ok {
		return r.Engine.RotateTileImage(tile, timg), nil
	}
	// Precache all tiles in tileset
	if tile.Tileset.Image == nil {
		for i := 0; i < len(tile.Tileset.Tiles); i++ {
			if tile.Tileset.Tiles[i].ID == tile.ID {
				sf, err := os.Open(tile.Tileset.GetFileFullPath(tile.Tileset.Tiles[i].Image.Source))
				if err != nil {
					return nil, err
				}
				defer sf.Close()
				timg, _, err = image.Decode(sf)
				if err != nil {
					return nil, err
				}
				r.TileCache[tile.Tileset.FirstGID+tile.ID] = timg
			}
		}
	} else {
		sf, err := os.Open(tile.Tileset.GetFileFullPath(tile.Tileset.Image.Source))
		if err != nil {
			return nil, err
		}
		defer sf.Close()

		img, _, err := image.Decode(sf)
		if err != nil {
			return nil, err
		}

		for i := uint32(0); i < uint32(tile.Tileset.TileCount); i++ {
			rect := tile.Tileset.GetTileRect(i)
			r.TileCache[i+tile.Tileset.FirstGID] = imaging.Crop(img, rect)
			if tile.ID == i {
				timg = r.TileCache[i+tile.Tileset.FirstGID]
			}
		}
	}

	return r.Engine.RotateTileImage(tile, timg), nil
}

func (r *Renderer) renderTile(layer *tiled.Layer, tile *tiled.LayerTile, x int, y int) error {
	if tile.IsNil() {
		return nil
	}

	img, err := r.getTileImage(tile)
	if err != nil {
		return err
	}

	pos := r.Engine.GetTilePosition(x, y)

	if layer.Opacity < 1 {
		mask := image.NewUniform(color.Alpha{uint8(layer.Opacity * 255)})

		draw.DrawMask(r.Result, pos, img, img.Bounds().Min, mask, mask.Bounds().Min, draw.Over)
	} else {
		draw.Draw(r.Result, pos, img, img.Bounds().Min, draw.Over)
	}

	return nil
}

func (r *Renderer) RenderLayerTiles(index int, tilepositions map[image.Point]bool) {
	layer := r.m.Layers[index]
	for point := range tilepositions {
		i := point.X + point.Y*r.m.Width
		tile := layer.Tiles[i]
		r.renderTile(layer, tile, point.X, point.Y)
	}
}

func (r *Renderer) RenderLayerRect(index, x, y, width, height int) error {
	layer := r.m.Layers[index]

	var xs, xe, xi, ys, ye, yi int
	if r.m.RenderOrder == "" || r.m.RenderOrder == "right-down" {
		xs = x
		xe = x + width
		xi = 1
		ys = y
		ye = y + height
		yi = 1
	} else {
		return ErrUnsupportedRenderOrder
	}

	if xs < 0 {
		xs = 0
	}
	if xe > r.m.Width {
		xe = r.m.Width
	}
	if ys < 0 {
		ys = 0
	}
	if ye > r.m.Height {
		ye = r.m.Height
	}

	var err error
	for y := ys; y*yi < ye; y = y + yi {
		i := xs + y*r.m.Width
		for x := xs; x*xi < xe; x = x + xi {
			err = r.renderTile(layer, layer.Tiles[i], x, y)
			if err != nil {
				return err
			}
			i++
		}
	}

	return nil
}

// RenderLayer renders single map layer.
func (r *Renderer) RenderLayer(index int) error {
	return r.RenderLayerRect(index, 0, 0, r.m.Width, r.m.Height)
}

// RenderVisibleLayers renders all visible map layers.
func (r *Renderer) RenderVisibleLayers() error {
	for i := range r.m.Layers {
		if !r.m.Layers[i].Visible {
			continue
		}

		if err := r.RenderLayer(i); err != nil {
			return err
		}
	}

	return nil
}

// Clear clears the render result to allow for separation of layers. For example, you can
// render a layer, make a copy of the render, clear the renderer, and repeat for each
// layer in the Map.
func (r *Renderer) Clear() {
	r.Result = image.NewNRGBA(r.Engine.GetFinalImageSize())
}

// SaveAsPng writes rendered layers as PNG image to provided writer.
func (r *Renderer) SaveAsPng(w io.Writer) error {
	return png.Encode(w, r.Result)
}

// SaveAsJpeg writes rendered layers as JPEG image to provided writer.
func (r *Renderer) SaveAsJpeg(w io.Writer, options *jpeg.Options) error {
	return jpeg.Encode(w, r.Result, options)
}

// SaveAsGif writes rendered layers as GIF image to provided writer.
func (r *Renderer) SaveAsGif(w io.Writer, options *gif.Options) error {
	return gif.Encode(w, r.Result, options)
}
