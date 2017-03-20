package main

import (
	"bitbucket.org/cia_rana/goapng"
	"image/png"
	"os"
	"fmt"
)

func main() {
	inPaths := []string{
		"res/gopher01.png",
		"res/gopher02.png",
		"res/gopher03.png",
	}
	outPath := "res/animated_gopher.png"
	
	// Assemble output image.
	outApng := &goapng.APNG{}
	for _, inPath := range inPaths {
		// Read image file.
		f, err := os.Open(inPath)
		if err != nil {
			fmt.Println(err)
			f.Close()
			return
		}
		inPng, err := png.Decode(f)
		if err != nil {
			fmt.Println(err)
			f.Close()
			return
		}
		f.Close()
		
		// Append a frame(type: *image.Image). First frame used as the default image.
		outApng.Image = append(outApng.Image, &inPng)

		// Append a delay time(type: uint32) per frame in 10 milliseconds.
		// If it is 0, the decoder renders the next frame as quickly as possible.
		outApng.Delay = append(outApng.Delay, 0)
	}

	// Encode images to APNG image.
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Println(err)
		f.Close()
		
		return
	}
	if err = goapng.EncodeAll(f, outApng); err != nil {
		fmt.Println(err)
		f.Close()
		os.Remove(outPath)
		return
	}
	f.Close()
}
