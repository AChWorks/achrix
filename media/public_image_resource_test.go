// SPDX-License-Identifier: MPL-2.0
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in cold process observations are deliberately absent from ordinary CI's
// repeated acceptance workload. Run one case per new process after inspecting
// this repository-owned fixture. They establish no hard RSS/latency SLA.
func TestPublicImageResourceObservation(t *testing.T) {
	scenario := os.Getenv("ACHRIX77_RESOURCE_SCENARIO")
	if scenario == "" {
		t.Skip("explicit cold resource observation not selected")
	}
	const width, height = 4096, 2048
	var source []byte
	mime := "image/jpeg"
	parallel := 1
	switch scenario {
	case "adam7":
		mime = "image/png"
		source = insertPNG(adam7PNG(t, width, height), pngChunk("eXIf", orientationExif(6, false)))
	case "progressive444":
		source = progressiveJPEG(width, height, "ycbcr")
	case "progressive-rgb":
		source = progressiveJPEG(width, height, "rgb")
	case "progressive-gray":
		source = progressiveJPEG(width, height, "gray")
	case "two-concurrent":
		source = progressiveJPEG(width, height, "rgb")
		parallel = 2
	default:
		t.Fatal("unknown resource scenario")
	}
	if mime == "image/jpeg" {
		source = append(append(append([]byte(nil), source[:2]...), jpegSegment(225, append([]byte("Exif\x00\x00"), orientationExif(6, false)...))...), source[2:]...)
	}
	if len(source) > int(MaxUploadBytes) {
		t.Fatal("fixture source limit")
	}
	f := newFixtureConfig(t, Config{MaxConns: 4, MaxOperations: 8})
	// Seed a valid ready original without running an image codec beforehand.
	// This disposable setup preserves exact source size/hash/dimensions and makes
	// the measured preparation's codec work cold in its own fresh process.
	asset := Asset{ID: newID(), Revision: 2, MIME: mime, Width: width, Height: height, Size: int64(len(source)), State: "ready"}
	sum := sha256.Sum256(source)
	asset.SHA256 = hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(f.root, asset.ID), source, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := f.db.Exec(context.Background(), "INSERT INTO media.assets(id,state,revision,filename,mime,size,width,height,sha256,created_at) VALUES($1,'ready',2,'resource',$2,$3,$4,$5,$6,now())", asset.ID, asset.MIME, asset.Size, asset.Width, asset.Height, asset.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	sourceSize := len(source)
	source = nil
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	type sample struct{ HeapAlloc, HeapInuse, RSS uint64 }
	startRSS := resourceRSS()
	peak := sample{before.HeapAlloc, before.HeapInuse, startRSS}
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				peak.HeapAlloc = max(peak.HeapAlloc, m.HeapAlloc)
				peak.HeapInuse = max(peak.HeapInuse, m.HeapInuse)
				peak.RSS = max(peak.RSS, resourceRSS())
			case <-stop:
				return
			}
		}
	}()
	start := time.Now()
	results := make(chan struct {
		image PublicImage
		err   error
	}, parallel)
	gate := make(chan struct{})
	entered := make(chan struct{}, parallel)
	begin := make(chan struct{})
	for i := 0; i < parallel; i++ {
		go func() {
			<-begin
			var dst io.Writer = io.Discard
			if parallel == 2 {
				dst = &resourceGateWriter{entered: entered, release: gate}
			}
			image, err := f.service.PreparePublicImage(context.Background(), publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, dst)
			results <- struct {
				image PublicImage
				err   error
			}{image, err}
		}()
	}
	close(begin)
	if parallel == 2 {
		for i := 0; i < 2; i++ {
			select {
			case <-entered:
			case result := <-results:
				close(gate)
				close(stop)
				<-sampled
				t.Fatal("concurrent prepare", result.err)
			case <-time.After(14 * time.Second):
				close(gate)
				close(stop)
				<-sampled
				t.Fatal("concurrent preparation deadline")
			}
		}
		if len(f.module.decoders) != 2 {
			t.Error("shared slots not retained through writer")
		}
		if _, err := f.service.PreparePublicImage(context.Background(), publicImageActor, PreparePublicImageRequest{asset.ID, asset.Revision}, io.Discard); err != ErrLimited {
			t.Error("third admission", err)
		}
		close(gate)
	}
	var outputs []PublicImage
	for i := 0; i < parallel; i++ {
		result := <-results
		if result.err != nil {
			t.Error(result.err)
		}
		outputs = append(outputs, result.image)
	}
	elapsed := time.Since(start)
	close(stop)
	<-sampled
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.GC()
	var reclaimed runtime.MemStats
	runtime.ReadMemStats(&reclaimed)
	record := struct {
		Scenario                                                                            string
		Width, Height, SourceBytes, Parallel                                                int
		ElapsedMS                                                                           int64
		BeforeHeap, PeakHeap, PeakHeapInuse, AllocatedBytes, AfterGCHeap, StartRSS, PeakRSS uint64
		Outputs                                                                             []PublicImage
	}{scenario, width, height, sourceSize, parallel, elapsed.Milliseconds(), before.HeapAlloc, peak.HeapAlloc, peak.HeapInuse, after.TotalAlloc - before.TotalAlloc, reclaimed.HeapAlloc, startRSS, peak.RSS, outputs}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ACHRIX77_RESOURCE=" + string(body))
}
func resourceRSS() uint64 {
	body, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(body))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

type resourceGateWriter struct {
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (w *resourceGateWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { w.entered <- struct{}{}; <-w.release })
	return len(p), nil
}
