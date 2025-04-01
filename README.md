# Fast-Crawl

Fast-Crawl is a high-performance web crawler designed to efficiently traverse and extract links from websites. It supports concurrent crawling and writing, making it suitable for large-scale web scraping tasks.

## Features

- Concurrent crawling with configurable number of crawlers and writers.
- Graceful shutdown handling.
- Error handling and logging.
- Configurable process timeout.
- Merges output from multiple writers into a single file.

## Installation

1. Clone the repository:

   ```bash
   git clone https://github.com/yourusername/fast-crawl.git
   cd fast-crawl
   ```
## Usage

Run the crawler with the default configuration:

```bash
./fast-crawl
```

## Configuration

The crawler can be configured by modifying the `Config` struct in `main.go`. Key configuration options include:

- `NumCrawlers`: Number of concurrent crawlers.
- `NumWriters`: Number of concurrent writers.
- `QueueSize`: Size of the link queue.
- `ProcessTimeout`: Timeout for processing each link.
- `InitialLinks`: List of initial URLs to start crawling from.
- `filename`: Name of the output file.

## How It Works

1. **Initialization**: The crawler initializes with the specified configuration and sets up channels for communication between crawlers and writers.

2. **Crawling**: Multiple crawler goroutines are started to process links concurrently. Each crawler fetches a URL, extracts links, and adds new links to the queue.

3. **Writing**: Writer goroutines write the crawled data to part files, which are later merged into a single output file.

4. **Error Handling**: Errors encountered during crawling and writing are logged and handled gracefully.

5. **Shutdown**: The crawler listens for shutdown signals and stops gracefully, ensuring all goroutines complete their tasks.

