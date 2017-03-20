package goapng

import (
	"io"
	"errors"
	"image"
	"image/png"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"compress/zlib"
)

type Encoder struct {
	CompressionLevel CompressionLevel
}

const (
	pngHeader = "\x89PNG\r\n\x1a\n"
	filterNum = 5 // none, sub, up, average, paeth
)

type CompressionLevel int

const (
	DefaultCompression CompressionLevel = 0
	NoCompression      CompressionLevel = -1
	BestSpeed          CompressionLevel = -2
	BestCompression    CompressionLevel = -3
)

func levelToZlib(l CompressionLevel) int {
	switch l {
	case DefaultCompression:
		return zlib.DefaultCompression
	case NoCompression:
		return zlib.NoCompression
	case BestSpeed:
		return zlib.BestSpeed
	case BestCompression:
		return zlib.BestCompression
	default:
		return zlib.DefaultCompression
	}
}

type idat []byte

func writeUint16(b []uint8, u uint16) {
	b[0] = uint8(u >> 8)
	b[1] = uint8(u)
} 

func writeUint32(b []uint8, u uint32) {
	b[0] = uint8(u >> 24)
	b[1] = uint8(u >> 16)
	b[2] = uint8(u >> 8)
	b[3] = uint8(u)
}

type APNG struct {
	Image []*image.Image // The successive images.
	Delay []uint16 // The successive delay times, one per frame, in 100ths of a second.
	Disposal []byte // The successive disposal methods, one per frame.
	LoopCount uint32 // The loop count. 0 indicates infinite looping.
	Config image.Config
}

type encoder struct {
	a *APNG
	w io.Writer
	seqNum uint32 // Sequence number of the animation chunk.
	
	tmpHeader [8]byte
	tmp [4 * 256]byte
	tmpFooter [4]byte

	ihdr []byte
	idats []idat

	err error
}

func (e *encoder) writeChunk(b []byte, name string) {
	if e.err != nil {
		return
	}
	
	// Write header (length, type).
	n := uint32(len(b))
	if int(n) != len(b) {
		e.err = errors.New("apng: chunk is too large")
		return
	}
	writeUint32(e.tmpHeader[:4], n)
	e.tmpHeader[4] = name[0]
	e.tmpHeader[5] = name[1]
	e.tmpHeader[6] = name[2]
	e.tmpHeader[7] = name[3]
	_, e.err = e.w.Write(e.tmpHeader[:8])
	if e.err != nil {
		return
	}

	// Write data.
	_, e.err = e.w.Write(b)
	if e.err != nil {
		return
	}

	// Write footer (crc).
	crc := crc32.NewIEEE()
	crc.Write(e.tmpHeader[4:8])
	crc.Write(b)
	writeUint32(e.tmpFooter[:4], crc.Sum32())
	_, e.err = e.w.Write(e.tmpFooter[:4])
}

func (e *encoder) writeIHDR() {
	e.writeChunk(e.ihdr, "IHDR")
}

func (e *encoder) writeacTL() {
	writeUint32(e.tmp[0:4], uint32(len(e.a.Image)))
	writeUint32(e.tmp[4:8], e.a.LoopCount)
	e.writeChunk(e.tmp[:8], "acTL")
}

func (e *encoder) writefcTL(frameIndex int) {
	// Write sequence_number.
	writeUint32(e.tmp[0:4], e.seqNum)

	bounds := (*e.a.Image[frameIndex]).Bounds()

	// Write width.
	writeUint32(e.tmp[4:8], uint32(bounds.Max.X - bounds.Min.X))
	
	// Write height.
	writeUint32(e.tmp[8:12], uint32(bounds.Max.Y - bounds.Min.Y))
	
	// Write x_offset.
	writeUint32(e.tmp[12:16], uint32(bounds.Min.X))

	// Write y_offset.
	writeUint32(e.tmp[16:20], uint32(bounds.Min.Y))

	// Write delay_num(numerator).
	writeUint16(e.tmp[20:22], e.a.Delay[frameIndex])

	// Write delay_den(denominator).
	writeUint16(e.tmp[22:24], uint16(100))
	
	// Write dispose_op.
	//switch d := e.a.Disposal[frameIndex]; d {
	//case 0, 1, 2:
	//	e.tmp[24] = d
	//default:
	//	e.tmp[24] = 0
	//}
	e.tmp[24] = 0

	// Write blend_op.
	e.tmp[25] = 0
	
	e.writeChunk(e.tmp[:26], "fcTL")
	e.seqNum++
}

func (e *encoder) writeIDATs() {
	for _, id := range e.idats {
		e.writeChunk(id, "IDAT")
	}
}

func (e *encoder) writefdATs() {
	for _, id := range e.idats {
		writeUint32(e.tmp[0:4], e.seqNum)
		fdat := make([]byte, 4, len(id))
		copy(fdat, e.tmp[0:4])
		fdat = append(fdat, id...)
		e.writeChunk(fdat, "fdAT")
		e.seqNum++
	}
}

func (e *encoder) writeIEND() {
	e.writeChunk(nil, "IEND")
}

const (
	dsStart = iota
	dsSeenIHDR
	dsSeenPLTE
	dsSeentRNS
	dsSeenIDAT
	dsSeenIEND
)

type chunkFetcher struct {
	bb *bytes.Buffer
	tmp [3 * 256]byte
	stage int

	pc *pngChunk
	ac *apngChunk
}

type pngChunk struct {
	ihdr []byte
	idats []idat
}

type apngChunk struct {
	ihdr []byte
}

func (c *chunkFetcher) parseIHDR(length uint32) error {
	_, err := io.ReadFull(c.bb, c.tmp[:length])
	if err != nil {
		return err
	}
	c.pc.ihdr = make([]byte, length)
	copy(c.pc.ihdr, c.tmp[:length])
	return nil
}

func (c *chunkFetcher) parseIDAT(length uint32) error {
	id := c.bb.Next(int(length))
	if len(id) < int(length) {
		return io.EOF
	}
	c.pc.idats = append(c.pc.idats, id)
	return nil
}

func (c *chunkFetcher) parseIEND(length uint32) error {
	return nil
}

func (c *chunkFetcher) parsePNGChunk() error {
	_, err := io.ReadFull(c.bb, c.tmp[:8])
	if err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(c.tmp[:4])
	
	switch string(c.tmp[4:8]) {
	case "IHDR":
		c.stage = dsSeenIHDR
		err =  c.parseIHDR(length)
	case "PLTE":
		// todo
	case "tRNS":
		// todo
	case "IDAT":
		c.stage = dsSeenIDAT
		err = c.parseIDAT(length)
	case "IEND":
		c.stage = dsSeenIEND
		err = c.parseIEND(length)
	}

	c.bb.Next(4) // Get rid of crc(4 bytes).
	return err
}

func fetchPNGChunk(bb *bytes.Buffer) (*pngChunk, error) {
	bb.Next(len(pngHeader))
	c := &chunkFetcher {
		bb: bb,
		stage: dsStart,
		pc: new(pngChunk),
	}
	
	for c.stage != dsSeenIEND {
		if err := c.parsePNGChunk(); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
	return c.pc, nil
}

func isSameColorModel(img []*image.Image) bool {
	if len(img) == 0 || (*img[0]) == nil {
		return false
	}

	reference := (*img[0]).ColorModel()
	for i := 1; i < len(img); i++ {
		if (*img[i]) == nil || (*img[i]).ColorModel() != reference {
			return false
		}
	}
	return true
}

func fullfillFrameRegionConstraints(img []*image.Image) bool {
	if len(img) == 0 || (*img[0]) == nil {
		return false
	}
	
	reference := (*img[0]).Bounds()

	// constraints:
	// 	x_offset >= 0 && y_offset >= 0
	if !(reference.Min.X >= 0 && reference.Min.Y >= 0) {
		return false
	}
	
	for i := 1; i < len(img); i++ {
		if (*img[i]) == nil {
			return false
		}

		bounds := (*img[i]).Bounds()
		
		// constrains:
		// 	   x_offset >= 0
		// 	&& y_offset >= 0
		// 	&& x_offset + width  = max_x <= first frame width
		// 	&& y_offset + height = max_y <= first frame height
		if !(bounds.Min.X >= 0 && bounds.Min.Y >= 0 && bounds.Max.X <= reference.Max.X && bounds.Max.Y <= reference.Max.Y) {
			return false
		}
	}
	return true
}

func EncodeAll(w io.Writer, a *APNG) error {
	if len(a.Image) == 0 {
		return errors.New("apng: need at least one image")
	}
	
	if len(a.Image) != len(a.Delay) {
		return errors.New("apng: mismatched image and delay lengths")
	}
	
	if a.Disposal != nil && len(a.Image) != len(a.Disposal) {
		return errors.New("apng: mismatch image and disposal lengths")
	}

	if !isSameColorModel(a.Image) {
		return errors.New("apng: must be all the same color model of images")
	}

	if !fullfillFrameRegionConstraints(a.Image) {
		return errors.New("apng: must fullfill frame region constraints.")
	}

	e := encoder{
		a: a,
		w: w,
	}
	
	_, e.err = io.WriteString(w, pngHeader)
	for i, img := range a.Image {
		bb := new(bytes.Buffer)
		if err := png.Encode(bb, *img); err != nil {
			return errors.New("apng: png encoding error(" + err.Error() + ")")
		}
		
		pc, err := fetchPNGChunk(bb)
		if err != nil {
			return err
		}
		e.ihdr = pc.ihdr
		e.idats = pc.idats

		// First image is defalt image.
		if i == 0 {
			e.writeIHDR()
			e.writeacTL()
			e.writefcTL(i)
			e.writeIDATs()
		} else {
			e.writefcTL(i)
			e.writefdATs()
		}
	}
	e.writeIEND()
	return e.err
}
