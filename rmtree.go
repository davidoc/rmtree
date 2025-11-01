package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	pflag "github.com/spf13/pflag"
)

var version = "dev"

type Metadata struct {
	VisibleName string `json:"visibleName"`
	Type        string `json:"type"`
	Parent      string `json:"parent"`
	Deleted     bool   `json:"deleted"`
}

type Item struct {
	UUID    string
	Name    string
	Type    string
	Parent  string
	DocType string
	SortKey string
}

type Config struct {
	Path       string
	OutputPath string
	ShowIcons  bool
	ShowLabels bool
	ShowUUID   bool
	UseColor   bool
	SymLink    bool
}

var colors = map[string]string{
	"folder": "\033[36m",
	"pdf":    "\033[31m",
	"epub":   "\033[32m",
	"reset":  "\033[0m",
}

func main() {
	config := parseArgs()

	if _, err := os.Stat(config.Path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: Path '%s' does not exist\n", config.Path)
		os.Exit(1)
	}

	if _, err := os.Stat(config.OutputPath); config.SymLink && os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: Output Path '%s' does not exist\n", config.OutputPath)
		os.Exit(1)
	}

	items, err := loadItems(config.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading items: %v\n", err)
		os.Exit(1)
	}

	children := buildChildrenMap(items)
	sortItems(items, children)

	if config.SymLink {
		linkTree(items, children, config)
	} else {
		printTree(items, children, config)
	}
}

func parseArgs() Config {
	config := Config{
		Path:       "/home/root/.local/share/remarkable/xochitl",
		OutputPath: ".",
		UseColor:   true,
	}

	pflag.BoolVarP(&config.ShowIcons, "icons", "i", false, "Show emoji icons")
	pflag.BoolVarP(&config.ShowLabels, "labels", "l", false, "Show document type labels")
	pflag.BoolVarP(&config.ShowUUID, "uuid", "u", false, "Show document UUIDs")
	noColor := pflag.BoolP("no-color", "n", false, "Disable colored output")
	showVersion := pflag.BoolP("version", "v", false, "Show version information")
	pflag.BoolVarP(&config.SymLink, "symlinks", "s", false, "Create symbolic links instead of printing")
	pflag.StringVarP(&config.OutputPath, "output", "o", ".", "Output path for symbolic links")
	pflag.Parse()

	if *showVersion {
		fmt.Println("rmtree version", version)
		os.Exit(0)
	}

	if pflag.NArg() > 0 {
		config.Path = pflag.Arg(0)
	}

	if *noColor {
		config.UseColor = false
	}

	return config
}

func loadItems(remarkablePath string) (map[string]*Item, error) {
	metadataFiles, err := filepath.Glob(filepath.Join(remarkablePath, "*.metadata"))
	if err != nil {
		return nil, err
	}

	items := make(map[string]*Item)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Load PDF and EPUB files for type detection
	pdfFiles, _ := filepath.Glob(filepath.Join(remarkablePath, "*.pdf"))
	epubFiles, _ := filepath.Glob(filepath.Join(remarkablePath, "*.epub"))

	pdfMap := make(map[string]bool)
	epubMap := make(map[string]bool)

	for _, f := range pdfFiles {
		uuid := strings.TrimSuffix(filepath.Base(f), ".pdf")
		pdfMap[uuid] = true
	}

	for _, f := range epubFiles {
		uuid := strings.TrimSuffix(filepath.Base(f), ".epub")
		epubMap[uuid] = true
	}

	// Process metadata files concurrently
	for _, metadataFile := range metadataFiles {
		wg.Add(1)
		go func(file string) {
			defer wg.Done()

			uuid := strings.TrimSuffix(filepath.Base(file), ".metadata")

			data, err := os.ReadFile(file)
			if err != nil {
				return
			}

			var metadata Metadata
			if err := json.Unmarshal(data, &metadata); err != nil {
				return
			}

			if metadata.Deleted {
				return
			}

			if metadata.VisibleName == "" {
				metadata.VisibleName = "Unnamed"
			}
			if metadata.Type == "" {
				metadata.Type = "DocumentType"
			}

			item := &Item{
				UUID:   uuid,
				Name:   metadata.VisibleName,
				Type:   metadata.Type,
				Parent: metadata.Parent,
			}

			// Determine document type
			if metadata.Type != "CollectionType" {
				if epubMap[uuid] {
					item.DocType = "epub"
				} else if pdfMap[uuid] {
					item.DocType = "pdf"
				} else {
					item.DocType = "notebook"
				}
			}

			// Create sort key: 0 for folders, 1 for documents, then name
			sortPrefix := "1"
			if metadata.Type == "CollectionType" {
				sortPrefix = "0"
			}
			item.SortKey = sortPrefix + "|" + metadata.VisibleName

			mu.Lock()
			items[uuid] = item
			mu.Unlock()
		}(metadataFile)
	}

	wg.Wait()
	return items, nil
}

func buildChildrenMap(items map[string]*Item) map[string][]*Item {
	children := make(map[string][]*Item)

	for _, item := range items {
		parent := item.Parent
		if parent == "" {
			parent = "root"
		}
		children[parent] = append(children[parent], item)
	}

	return children
}

func sortItems(items map[string]*Item, children map[string][]*Item) {
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			return children[parent][i].SortKey < children[parent][j].SortKey
		})
	}
}

func printTree(items map[string]*Item, children map[string][]*Item, config Config) {
	fmt.Println(".")

	roots := children["root"]
	trashItems := children["trash"]

	dirCount := 0
	fileCount := 0

	for _, item := range items {
		if item.Type == "CollectionType" {
			dirCount++
		} else {
			fileCount++
		}
	}

	// Print root items
	for i, item := range roots {
		isLast := i == len(roots)-1 && len(trashItems) == 0
		printItem(item, "", isLast, 0, children, config)
	}

	// Print trash items
	if len(trashItems) > 0 {
		dirCount++ // Add trash folder to count

		connector := "└── "
		icon := ""
		if config.ShowIcons {
			icon = "📁 "
		}

		color := ""
		colorReset := ""
		if config.UseColor {
			color = colors["folder"]
			colorReset = colors["reset"]
		}

		fmt.Printf("%s%s%sTrash%s\n", connector, color, icon, colorReset)

		for i, item := range trashItems {
			isLast := i == len(trashItems)-1
			printTrashItem(item, "    ", isLast, 1, config)
		}
	}

	fmt.Println()

	// Print summary
	dirText := "directories"
	if dirCount == 1 {
		dirText = "directory"
	}

	fileText := "files"
	if fileCount == 1 {
		fileText = "file"
	}

	fmt.Printf("%d %s, %d %s\n", dirCount, dirText, fileCount, fileText)
}

func printItem(item *Item, prefix string, isLast bool, depth int, children map[string][]*Item, config Config) {
	if depth > 50 {
		return
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}

	icon, color, typeLabel, uuidDisplay := getItemFormatting(item, config)

	fmt.Printf("%s%s%s%s%s%s%s%s\n", prefix, connector, color, icon, item.Name, colors["reset"], typeLabel, uuidDisplay)

	// Print children
	itemChildren := children[item.UUID]
	for i, child := range itemChildren {
		childIsLast := i == len(itemChildren)-1

		newPrefix := prefix
		if isLast {
			newPrefix += "    "
		} else {
			newPrefix += "│   "
		}

		printItem(child, newPrefix, childIsLast, depth+1, children, config)
	}
}

func printTrashItem(item *Item, prefix string, isLast bool, depth int, config Config) {
	if depth > 50 {
		return
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}

	icon, color, typeLabel, uuidDisplay := getItemFormatting(item, config)

	fmt.Printf("%s%s%s%s%s%s%s%s\n", prefix, connector, color, icon, item.Name, colors["reset"], typeLabel, uuidDisplay)
}

func getItemFormatting(item *Item, config Config) (icon, color, typeLabel, uuidDisplay string) {
	if config.UseColor {
		if item.Type == "CollectionType" {
			color = colors["folder"]
		} else {
			switch item.DocType {
			case "pdf":
				color = colors["pdf"]
			case "epub":
				color = colors["epub"]
			}
		}
	}

	if config.ShowIcons {
		if item.Type == "CollectionType" {
			icon = "📁 "
		} else {
			switch item.DocType {
			case "pdf":
				icon = "📕 "
			case "epub":
				icon = "📗 "
			default:
				icon = "📓 "
			}
		}
	}

	if config.ShowLabels && item.Type != "CollectionType" {
		switch item.DocType {
		case "pdf":
			typeLabel = " (pdf)"
		case "epub":
			typeLabel = " (epub)"
		default:
			typeLabel = " (notebook)"
		}
	}

	if config.ShowUUID && item.Type != "CollectionType" {
		uuidDisplay = " [" + item.UUID + "]"
	}

	return
}

// Create symbolic links of the flat structure into a tree structure of filesystem files and directories.
func linkTree(items map[string]*Item, children map[string][]*Item, config Config) {
	roots := children["root"]
	trashItems := children["trash"]

	dirCount := 0
	fileCount := 0

	for _, item := range items {
		if item.Type == "CollectionType" {
			dirCount++
		} else {
			fileCount++
		}
	}

	// Link root items
	for i, item := range roots {
		isLast := i == len(roots)-1 && len(trashItems) == 0
		linkItem(item, "", isLast, 0, children, config)
	}

	// Print summary
	dirText := "directories"
	if dirCount == 1 {
		dirText = "directory"
	}

	fileText := "files"
	if fileCount == 1 {
		fileText = "file"
	}

	fmt.Printf("%d %s, %d %s\n", dirCount, dirText, fileCount, fileText)
}

func linkItem(item *Item, prefix string, isLast bool, depth int, children map[string][]*Item, config Config) {
	if depth > 50 {
		return
	}

	itemName := item.Name
	//Remove leading and trailing space from directory name
	itemName = strings.Trim(itemName, " ")

	// Create directory or symlink
	if item.Type == "CollectionType" {
		// Create directory
		dirPath := filepath.Join(config.OutputPath, prefix, itemName)
		err := os.MkdirAll(dirPath, os.ModePerm)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory '%s': %v\n", dirPath, err)
			return
		}
		// fmt.Fprintf(os.Stdout, "Created directory '%s'\n", dirPath)
	} else if item.Type == "DocumentType" {
		// Create symlink
		srcPath := ""
		switch item.DocType {
		case "epub":
			srcPath = filepath.Join(config.Path, item.UUID+".epub")
		case "pdf":
			srcPath = filepath.Join(config.Path, item.UUID+".pdf")
		default:
			return // Skip for symlinking
		}

		destDir := filepath.Join(config.OutputPath, prefix)
		_, err := os.Stat(destDir)
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: Path '%s' does not exist\n", destDir)
			return
		}

		fileName := itemName
		// Sanitize filename
		fileName = strings.ReplaceAll(fileName, string(os.PathSeparator), "_")
		// Append file extension if missing
		if !strings.HasSuffix(fileName, "."+item.DocType) {
			fileName += "." + item.DocType
		}

		destPath := filepath.Join(destDir, fileName)

		err = createOrReplaceSymlink(srcPath, destPath)

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating symlink from '%s' to '%s': %v\n", srcPath, destPath, err)
			return
		}
		// fmt.Fprintf(os.Stdout, "Created symlink from '%s' to '%s'\n", srcPath, destPath)
	}

	// Link children
	itemChildren := children[item.UUID]
	for i, child := range itemChildren {
		childIsLast := i == len(itemChildren)-1

		newPrefix := prefix
		newPrefix += itemName + string(os.PathSeparator)

		linkItem(child, newPrefix, childIsLast, depth+1, children, config)
	}
}

// createOrReplaceSymlink creates a symlink, replacing an existing symlink at linkPath if present.
// It will not remove a regular file/dir unless you want that behaviour.
func createOrReplaceSymlink(target, linkPath string) error {
	// if a symlink exists, remove it
	if fi, err := os.Lstat(linkPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(linkPath); err != nil {
				return err
			}
		} else {
			// exists and is not a symlink — decide whether to fail or remove
			return fmt.Errorf("path exists and is not a symlink: %s", linkPath)
		}
	}
	return os.Symlink(target, linkPath)
}
