package main

import (
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
	NumCrawlers    int
	NumWriters     int
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
	wgCrawler  sync.WaitGroup
	wgWtiter   sync.WaitGroup
	seen       *sync.Map
	idCounter  *int64
	ctx        context.Context
	cancel     context.CancelFunc
	errorsChan chan error
	nodes      chan Node
	st         SafeTime
}

type SafeTime struct {
	mu   sync.Mutex
	time time.Time
}

func (s *SafeTime) TryWrite() {
	if s.mu.TryLock() {
		defer s.mu.Unlock()
		// Simulate some work
		s.time = time.Now()
		// fmt.Println("Time written:", s.time)
	} else {
		// fmt.Println("Skipped writing, mutex is locked")
	}
}

func (s *SafeTime) GetDelay() float64 {
	return time.Since(s.time).Seconds()
}

func (c *Crawler) log(id int64) {
	fmt.Printf("Processed: %d/ Total added: %d\n", id, *c.idCounter)
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
		errorsChan: make(chan error, config.NumCrawlers+config.NumWriters),
		nodes:      make(chan Node, config.QueueSize),
		st:         SafeTime{time: time.Now()},
	}
}

func (c *Crawler) MergeFiles() {
	// merge all the files created by writers
	var files []string
	for i := 0; i < c.config.NumWriters; i++ {
		files = append(files, c.config.filename+".part"+strconv.Itoa(i))
	}

	outputFile, err := os.Create(c.config.filename)
	if err != nil {
		log.Fatalf("Failed to create output file: %v", err)
	}
	defer outputFile.Close()

	for _, file := range files {
		partFile, err := os.Open(file)
		if err != nil {
			log.Fatalf("Failed to open part file %s: %v", file, err)
		}

		_, err = io.Copy(outputFile, partFile)
		if err != nil {
			log.Fatalf("Failed to copy part file %s: %v", file, err)
		}

		partFile.Close()
	}
	// for _, file := range files {
	// 	err := os.Remove(file)
	// 	if err != nil {
	// 		log.Printf("Failed to remove part file %s: %v", file, err)
	// 	}
	// }
}

// Start begins the crawling process
func (c *Crawler) Start() error {
	// Start error handler
	go c.handleErrors()

	// Start workers
	for i := 0; i < c.config.NumCrawlers; i++ {
		c.wgCrawler.Add(1)
		go c.crawl(i)
	}
	for i := 0; i < c.config.NumWriters; i++ {
		c.wgWtiter.Add(1)
		go c.writeNodes(i)
	}
	// Add initial links
	for _, initialLinkString := range c.config.InitialLinks {
		link := LinkData{
			BaseURL:  initialLinkString,
			Path:     "",
			ParentId: 0,
			Id:       c.getNewId(),
		}
		select {
		case c.linkQueue <- link:
			log.Printf("Added initial link: %s\n", link)
		case <-c.ctx.Done():
			return fmt.Errorf("crawler stopped while adding initial links")
		}
	}
	c.wgCrawler.Wait()
	fmt.Println("Crawling done")

	close(c.nodes)
	c.wgWtiter.Wait()
	fmt.Println("Writing done")

	c.MergeFiles()
	fmt.Println("Merging done")
	// Handle graceful shutdown
	c.handleShutdown()

	close(c.errorsChan)

	return nil
}

func (c *Crawler) writeNodes(id int) {
	defer c.wgWtiter.Done()
	f, err := os.Create(c.config.filename + ".part" + strconv.Itoa(id))
	if err != nil {
		log.Fatalf("Error creating file: %v", err)
	}
	defer f.Close()
	for node := range c.nodes {
		fmt.Fprintf(f, "%d###%s###%s###%t###%d\n", node.Id, node.Name, node.Path, node.IsFile, node.ParentId)
		if err != nil {
			log.Fatalf("Error writing to file: %v", err)
		}
	}
}

func (c *Crawler) crawl(id int) {
	defer c.wgCrawler.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case link, ok := <-c.linkQueue:
			if !ok {
				return
			}
			c.st.TryWrite()
			if err := c.processLink(link); err != nil {
				select {
				case c.errorsChan <- fmt.Errorf("worker %d error: %v", id, err):
				default:
					log.Printf("Error channel full, dropping error: %v", err)
				}
			}
		default:
			// HACK:
			if c.st.GetDelay() > c.config.ProcessTimeout.Seconds()+10 {
				c.cancel()
				fmt.Printf("Idle for more than %f seconds.", c.config.ProcessTimeout.Seconds()+10)
			}
		}
	}
}

func isIntegral(val float64) bool {
	return val == float64(int(val))
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
	if link.Id%1000 == 0 {
		c.log(link.Id)
	}
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
	return in, out
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
		return strings.HasSuffix(link.Path, v)
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
				Id:       c.getNewId(),
			})
		}
	})
	return links, nil
}

func (c *Crawler) getNewId() int64 {
	return atomic.AddInt64(c.idCounter, 1)
}

func main() {
	initialURLs := []string{}

	for i := 1; i < 18; i++ {
		if i == 2 {
			continue
		}
		baseURL := "http://ftp" + strconv.Itoa(i) + ".circleftp.net"
		initialURLs = append(initialURLs, baseURL)
	}
	config := Config{
		NumCrawlers:    50,
		NumWriters:     40,
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
