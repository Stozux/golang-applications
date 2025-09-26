package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Descobre o tamanho do arquivo
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

// Limita o uso de rede
type RateLimiter struct {
	tokens chan struct{}
}

func NewRateLimiter(bytesPerSec int64) *RateLimiter {
	rl := &RateLimiter{tokens: make(chan struct{}, bytesPerSec)}

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for i := int64(0); i < bytesPerSec; i++ {
				select {
				case rl.tokens <- struct{}{}:
				default:
				}
			}
		}
	}()
	return rl
}

func (rl *RateLimiter) Wait(n int) {
	for i := 0; i < n; i++ {
		<-rl.tokens
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

// Baixa os chunks
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

func main() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("> Digite a URL do arquivo: ")
	url, _ := reader.ReadString('\n')
	url = strings.TrimSpace(url)

	fmt.Print("ATENÇÃO: Um número muito grande de Threads pode acarretar em erros de requisição ou lentidão.\n")
	fmt.Print("> Digite a quantidade de threads/chunks: ")
	var threads int64
	fmt.Scanln(&threads)

	fmt.Print("> Digite a velocidade máxima de download (MB/s): ")
	var limitMB int64
	fmt.Scanln(&limitMB)

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

	outFile, err := os.Create(path.Base(url))
	if err != nil {
		log.Println("Erro criando arquivo final:", err)
		return
	}
	defer outFile.Close()

	if err := outFile.Truncate(fileSize); err != nil {
		log.Println("Erro ajustando tamanho do arquivo:", err)
		return
	}

	rl := NewRateLimiter(limitMB * 1024 * 1024)

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
	log.Printf("Download concluído! Arquivo salvo como %s\n", path.Base(url))
}
