package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Config holds the crawler configuration
type Config struct {
	NumWorkers     int
	QueueSize      int
	InitialLinks   []string
	ProcessTimeout time.Duration
	filename       string
}

// File/ directory details
type Node struct {
	Id       int64  // Unique ID for the node
	Name     string // Name of the directory or file
	Path     string // Full path (URL)
	IsFile   bool   // True if the node represents a file
	ParentId int64  // Parent node ID
}
type LinkData struct {
	BaseURL  string
	Path     string
	ParentId int64
	Id       int64
}

// Crawler manages the crawling process
type Crawler struct {
	config     Config
	linkQueue  chan LinkData
	wg         sync.WaitGroup
	seen       *sync.Map
	idCounter  *int64
	ctx        context.Context
	cancel     context.CancelFunc
	errorsChan chan error
	nodes      chan Node
}

// NewCrawler creates a new crawler instance
func NewCrawler(config Config) *Crawler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Crawler{
		config:     config,
		linkQueue:  make(chan LinkData, config.QueueSize),
		seen:       &sync.Map{},
		idCounter:  new(int64),
		ctx:        ctx,
		cancel:     cancel,
		errorsChan: make(chan error, config.NumWorkers),
		nodes:      make(chan Node),
	}
}

// Start begins the crawling process
func (c *Crawler) Start() error {
	// Start error handler
	go c.handleErrors()

	// Start workers
	for i := 0; i < c.config.NumWorkers; i++ {
		c.wg.Add(1)
		go c.worker(i)
	}

	go c.writeNodesToFile()
	// Add initial links
	for _, initialLinkString := range c.config.InitialLinks {
		link := LinkData{
			BaseURL:  initialLinkString,
			Path:     "",
			ParentId: 0,
			Id:       atomic.AddInt64(c.idCounter, 1),
		}
		select {
		case c.linkQueue <- link:
			log.Printf("Added initial link: %s\n", link)
		case <-c.ctx.Done():
			return fmt.Errorf("crawler stopped while adding initial links")
		}
	}

	// Handle graceful shutdown
	c.handleShutdown()

	// Wait for completion
	c.wg.Wait()
	close(c.nodes)
	close(c.errorsChan)

	return nil
}

func (c *Crawler) worker(id int) {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case link, ok := <-c.linkQueue:
			if !ok {
				return
			}
			if err := c.processLink(link); err != nil {
				select {
				case c.errorsChan <- fmt.Errorf("worker %d error: %v", id, err):
				default:
					log.Printf("Error channel full, dropping error: %v", err)
				}
			}
		}
	}
}

func (c *Crawler) processLink(link LinkData) error {
	// Check for duplicates
	if _, exists := c.seen.LoadOrStore(link, struct{}{}); exists {
		return nil
	}

	// Create timeout context for processing
	ctx, cancel := context.WithTimeout(c.ctx, c.config.ProcessTimeout)
	defer cancel()

	// Generate unique ID

	// Process the link with timeout
	newLinks, err := c.processWithTimeout(ctx, link)
	if err != nil {
		return fmt.Errorf("processing link %s: %v", link, err)
	}
	// Add new links in a separate goroutine
	go c.addNewLinks(newLinks)

	return nil
}

func (c *Crawler) processWithTimeout(ctx context.Context, link LinkData) ([]LinkData, error) {
	done := make(chan []LinkData, 1)
	errChan := make(chan error, 1)

	go func() {
		links, err := c.extractLinks(link)
		if err != nil {
			errChan <- err
			return
		}
		done <- links
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("processing timed out")
	case err := <-errChan:
		return nil, err
	case result := <-done:
		return result, nil
	}
}

// isDirectory checks if a URL is a directory based on trailing slash.
func isDirectory(link LinkData) bool {
	return strings.HasSuffix(link.Path, "/")
}

func filter[T any](ss []T, test func(T) bool) (in []T, out []T) {
	for _, s := range ss {
		if test(s) {
			in = append(in, s)
		} else {
			out = append(out, s)
		}
	}
	return
}

func contain(link LinkData) bool {
	var ignoreList []string = []string{
		"..",
		"?C=N;O=D",
		"?C=M;O=A",
		"?C=S;O=A",
		"?C=D;O=A",
		"ip.php",
	}
	for _, v := range ignoreList {
		if v == link.Path {
			return true
		}
	}
	return false
}

func isAbsolute(link LinkData) bool {
	// checks if the link is Relative
	if strings.HasPrefix(link.Path, "http://") || strings.HasPrefix(link.Path, "https://") {
		return true
	} else {
		return false
	}
}

func stringToLink(baseURL string, link string) LinkData {
	return LinkData{
		BaseURL: baseURL,
		Path:    link,
	}
}

func toTitle(str string) string {
	// capitalize the first letter using ToUpper
	if str == "" {
		return str
	}
	return strings.ToUpper(string(str[0])) + str[1:]
}

func linkToName(link LinkData) string {
	// get the last part of the link after the last /
	path := link.Path
	lastSlash := strings.LastIndex(path, "/")
	lastPart := path[lastSlash+1:]
	// unescape the last part
	name, _ := url.QueryUnescape(lastPart)
	// capitalize the first letter
	// Use golang.org/x/text/cases to capitalize the first letter
	name = toTitle(name)
	return name
}

func (c *Crawler) linkToNode(link LinkData) Node {
	fullPath := link.BaseURL + link.Path
	isDir := isDirectory(link)
	return Node{
		Id:       link.Id,
		Name:     linkToName(link),
		Path:     fullPath,
		IsFile:   !isDir,
		ParentId: link.ParentId,
	}
}

func (c *Crawler) addNewLinks(links []LinkData) {
	_, links = filter(links, contain)
	_, links = filter(links, isAbsolute)

	for _, link := range links {
		node := c.linkToNode(link)
		c.nodes <- node
	}

	directories, _ := filter(links, isDirectory)

	for _, link := range directories {
		// fmt.Println("Added link to queue: ", link)
		select {
		case c.linkQueue <- link:
		//	log.Printf("Added new link to queue: %s\n", link)
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Crawler) handleErrors() {
	for err := range c.errorsChan {
		log.Printf("Error: %v\n", err)
	}
}

func (c *Crawler) handleShutdown() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signals
		log.Println("Shutdown signal received")
		c.cancel()
	}()
}

// extractLinks simulates link extraction with error handling
func (c *Crawler) extractLinks(link LinkData) ([]LinkData, error) {
	// Simulate potential errors
	if link.BaseURL == "" && link.Path == "" {
		return nil, fmt.Errorf("empty link provided")
	}
	fullPath := link.BaseURL + link.Path
	resp, err := http.Get(fullPath)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch URL %s: %s", link, resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var links []LinkData
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists {
			links = append(links, LinkData{
				BaseURL:  link.BaseURL,
				Path:     href,
				ParentId: link.Id,
				Id:       atomic.AddInt64(c.idCounter, 1),
			})
		}
	})
	return links, nil
}

func (c *Crawler) writeToFile(node Node) error {
	// Open the file in append mode, create it if it doesn't exist
	file, err := os.OpenFile("output.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Write node information to the file
	_, err = fmt.Fprintf(file, "%d###%s###%s###%t###%d\n",
		node.Id, node.Name, node.Path, node.IsFile, node.ParentId)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	return nil
}

func (c *Crawler) writeNodesToFile() {
	const (
		bufferSize = 1024 * 1024 * 8 // 8MB buffer per writer
		batchSize  = 10000           // Nodes per batch
		numWriters = 4               // Number of concurrent writers
	)

	var wg sync.WaitGroup
	nodeChannels := make([]chan Node, numWriters)

	// Create buffered channels for each writer
	for i := range nodeChannels {
		nodeChannels[i] = make(chan Node, batchSize)
	}

	// Start multiple writer goroutines
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerNum int, nodeChan chan Node) {
			defer wg.Done()

			filename := fmt.Sprintf("%s.part%d", c.config.filename, writerNum)
			file, err := os.OpenFile(filename,
				os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Printf("Writer %d failed to open file: %v", writerNum, err)
				return
			}
			defer file.Close()

			writer := bufio.NewWriterSize(file, bufferSize)
			defer writer.Flush()

			batch := make([]Node, 0, batchSize)

			for node := range nodeChan {
				batch = append(batch, node)

				if len(batch) >= batchSize {
					for _, n := range batch {
						fmt.Fprintf(writer, "%d###%s###%s###%t###%d\n",
							n.Id, n.Name, n.Path, n.IsFile, n.ParentId)
					}
					batch = batch[:0]
				}
			}

			// Write remaining nodes in batch
			for _, n := range batch {
				fmt.Fprintf(writer, "%d###%s###%s###%t###%d\n",
					n.Id, n.Name, n.Path, n.IsFile, n.ParentId)
			}
			writer.Flush()
		}(i, nodeChannels[i])
	}

	// Distribute nodes across writers
	nodeCount := 0
	for node := range c.nodes {
		writerIndex := nodeCount % numWriters
		nodeChannels[writerIndex] <- node
		nodeCount++
	}

	// Close all channels
	for _, ch := range nodeChannels {
		close(ch)
	}

	// Wait for all writers to finish
	wg.Wait()

	// Merge files at the end
	c.mergeFiles(numWriters)
}

func (c *Crawler) mergeFiles(numParts int) error {
	finalFile, err := os.Create(c.config.filename)
	if err != nil {
		return fmt.Errorf("failed to create final file: %v", err)
	}
	defer finalFile.Close()

	writer := bufio.NewWriterSize(finalFile, 1024*1024*8)
	defer writer.Flush()

	// Calculate total size for progress reporting
	var totalSize int64
	for i := 0; i < numParts; i++ {
		partFilename := fmt.Sprintf("%s.part%d", c.config.filename, i)
		if info, err := os.Stat(partFilename); err == nil {
			totalSize += info.Size()
		}
	}

	var processedSize int64
	for i := 0; i < numParts; i++ {
		partFilename := fmt.Sprintf("%s.part%d", c.config.filename, i)

		partFile, err := os.OpenFile(partFilename, os.O_RDONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open part file %s: %v", partFilename, err)
		}

		reader := bufio.NewReaderSize(partFile, 1024*1024*8)

		// Create a progress-tracking reader
		// partSize, _ := partFile.Seek(0, io.SeekEnd)
		partFile.Seek(0, io.SeekStart)

		// Copy with progress tracking
		copied, err := io.Copy(writer, io.TeeReader(reader, &progressWriter{
			processed: &processedSize,
			total:     totalSize,
		}))

		partFile.Close()

		if err != nil {
			return fmt.Errorf("failed to copy from part file %s: %v", partFilename, err)
		}

		log.Printf("Merged part %d/%d (%.2f MB)", i+1, numParts, float64(copied)/1024/1024)

		if err := os.Remove(partFilename); err != nil {
			log.Printf("Warning: failed to remove part file %s: %v", partFilename, err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush final file: %v", err)
	}

	log.Printf("Merge completed: %.2f MB total", float64(totalSize)/1024/1024)
	return nil
}

// progressWriter tracks copy progress
type progressWriter struct {
	processed *int64
	total     int64
	lastLog   time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	atomic.AddInt64(pw.processed, int64(n))

	// Log progress every second
	if time.Since(pw.lastLog) > time.Second {
		progress := float64(*pw.processed) / float64(pw.total) * 100
		log.Printf("Merging progress: %.1f%% (%.2f/%.2f MB)",
			progress,
			float64(*pw.processed)/1024/1024,
			float64(pw.total)/1024/1024)
		pw.lastLog = time.Now()
	}

	return n, nil
}

func main() {
	initialURLs := []string{}

	for i := 1; i < 20; i++ {
		baseURL := "http://ftp" + strconv.Itoa(i) + ".circleftp.net"
		initialURLs = append(initialURLs, baseURL)
	}
	config := Config{
		NumWorkers:     30,
		QueueSize:      300,
		ProcessTimeout: 20 * time.Second,
		InitialLinks:   initialURLs,
		filename:       "data.db",
	}

	crawler := NewCrawler(config)
	if err := crawler.Start(); err != nil {
		log.Fatalf("Crawler error: %v", err)
	}
	log.Println("Crawling completed successfully")
}
