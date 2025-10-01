package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

func getFileName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "output.dat"
	}

	fileName := path.Base(u.Path)

	if ext := u.Query().Get("format"); ext != "" {
		fileName = strings.TrimSuffix(fileName, path.Ext(fileName)) + "." + ext
	}

	return fileName
}

func getFileSize(url string) (int64, error) {
	resp, err := http.Head(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.Header.Get("Accept-Ranges") != "bytes" {
		return 0, fmt.Errorf("servidor não suporta downloads parciais (range requests)")
	}

	sizeStr := resp.Header.Get("Content-Length")
	if sizeStr == "" {
		return 0, fmt.Errorf("servidor não retornou Content-Length")
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0, err
	}

	return size, nil
}

// RateLimiter usando mutex
type RateLimiter struct {
	bytesPerSec int64
	mu          sync.Mutex
	tokens      int64
	lastRefill  time.Time
}

func NewRateLimiter(bytesPerSec int64) *RateLimiter {
	return &RateLimiter{
		bytesPerSec: bytesPerSec,
		tokens:      bytesPerSec,
		lastRefill:  time.Now(),
	}
}

func (rl *RateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()

	newTokens := int64(elapsed * float64(rl.bytesPerSec))
	if newTokens > 0 {
		rl.tokens += newTokens
		if rl.tokens > rl.bytesPerSec {
			rl.tokens = rl.bytesPerSec
		}
		rl.lastRefill = now
	}
}

func (rl *RateLimiter) Wait(n int) {
	for {
		rl.mu.Lock()
		rl.refill()
		if rl.tokens >= int64(n) {
			rl.tokens -= int64(n)
			rl.mu.Unlock()
			break
		}
		rl.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
}

type rateLimitedReader struct {
	r  io.Reader
	rl *RateLimiter
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	if len(p) > 16*1024 {
		p = p[:16*1024]
	}
	r.rl.Wait(len(p))
	return r.r.Read(p)
}

func downloadChunk(url string, start, end int64, file *os.File, wg *sync.WaitGroup, rl *RateLimiter) {
	defer wg.Done()

	log.Printf("Baixando chunk %d-%d\n", start, end)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("Erro criando requisição:", err)
		return
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Erro no download:", err)
		return
	}
	defer resp.Body.Close()

	_, err = file.WriteAt([]byte{}, start)
	if err != nil {
		log.Println("Erro preparando offset:", err)
		return
	}

	limitedReader := &rateLimitedReader{r: resp.Body, rl: rl}

	_, err = io.Copy(&sectionWriter{file: file, offset: start}, limitedReader)
	if err != nil {
		log.Println("Erro copiando chunk:", err)
		return
	}

	log.Printf("Chunk %d-%d baixado\n", start, end)
}

type sectionWriter struct {
	file   *os.File
	offset int64
}

func (sw *sectionWriter) Write(p []byte) (int, error) {
	n, err := sw.file.WriteAt(p, sw.offset)
	sw.offset += int64(n)
	return n, err
}

func runDownload(url string, threads int64, limitMB int64) {
	log.Println("=============================")
	log.Println("Download em lotes de arquivos")
	log.Println("=============================")
	log.Println("URL do arquivo:", url)

	log.Println("Obtendo tamanho do arquivo...")
	fileSize, err := getFileSize(url)
	if err != nil {
		log.Println("Erro:", err)
		return
	}
	log.Println("Tamanho do arquivo:", fileSize, "bytes")

	chunkSize := (fileSize + threads - 1) / threads
	chunks := (fileSize + chunkSize - 1) / chunkSize
	log.Printf("Dividindo em %d chunks, cada um até %d bytes\n", chunks, chunkSize)

	fileName := getFileName(url)
	outFile, err := os.Create(fileName)
	if err != nil {
		log.Println("Erro criando arquivo final:", err)
		return
	}
	defer outFile.Close()

	if err := outFile.Truncate(fileSize); err != nil {
		log.Println("Erro ajustando tamanho do arquivo:", err)
		return
	}

	rl := NewRateLimiter(limitMB * 1024 * 1024) // Convert MB/s para bytes/s

	var wg sync.WaitGroup

	for i := int64(0); i < chunks; i++ {
		start := i * chunkSize
		end := (i+1)*chunkSize - 1
		if end >= fileSize {
			end = fileSize - 1
		}

		wg.Add(1)
		go downloadChunk(url, start, end, outFile, &wg, rl)
	}

	wg.Wait()
	log.Printf("Download concluído! Arquivo salvo como %s\n", fileName)
}

func main() {
	if len(os.Args) < 4 {
		fmt.Printf("Uso: %s <url> <threads> <limiteMB>\n", os.Args[0])
		os.Exit(1)
	}

	url := os.Args[1]

	threads, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil || threads <= 0 {
		log.Fatalln("Número de threads inválido:", os.Args[2])
	}

	limitMB, err := strconv.ParseInt(os.Args[3], 10, 64)
	if err != nil || limitMB <= 0 {
		log.Fatalln("Limite de MB/s inválido:", os.Args[3])
	}

	var total time.Duration
	const runs = 30

	for i := 0; i < runs; i++ {
		start := time.Now()
		log.Printf("Execução %d/%d\n", i+1, runs)
		runDownload(url, threads, limitMB)
		duration := time.Since(start)
		log.Printf("Tempo execução %d: %s\n", i+1, duration)
		total += duration

		// Remove o arquivo para próxima execução
		os.Remove(getFileName(url))
	}

	log.Printf("Tempo médio das %d execuções: %s\n", runs, total/time.Duration(runs))
}

//a
