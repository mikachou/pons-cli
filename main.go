package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"database/sql"

	"github.com/BurntSushi/toml"
	"github.com/adrg/xdg"
	"github.com/chzyer/readline"
	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"golang.org/x/net/html"
	"golang.org/x/term"

	_ "github.com/mattn/go-sqlite3"
)

const baseURL = "https://api.pons.com/v1/"

const dictionaryURL = baseURL + "dictionary"
const dictionariesURL = baseURL + "dictionaries"

type Config struct {
	APIKey          string `toml:"api_key"`
	CacheTTL        int    `toml:"cache_ttl"`
	CmdHistoryLimit int    `toml:"cmd_history_limit"`
}

var config Config
var currentDict string
var db *sql.DB

// Dictionary represents a single dictionary from the PONS API

type Dictionary struct {
	Key         string   `json:"key"`
	SimpleLabel string   `json:"simple_label"`
	Languages   []string `json:"languages"`
}

// Translation-related structs
type TranslationResponse []struct {
	Lang string `json:"lang"`
	Hits []Hit  `json:"hits"`
}

type Hit struct {
	Roms   []Rom  `json:"roms"`
	Source string `json:"source"`
	Target string `json:"target"`
}

type Rom struct {
	Headword string `json:"headword"`
	Arabs    []Arab `json:"arabs"`
}

type Arab struct {
	Header       string        `json:"header"`
	Translations []Translation `json:"translations"`
}

type Translation struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

const welcomeMessage = `
To use the pons-cli app, you must first configure your PONS API key.

Please enter:
  .set api_key <your_api_key>

If you don’t have an API key, visit:
  https://en.pons.com/open_dict/public_api

Note: You may need to create an account on the PONS website.
`

func main() {
	if err := setup(); err != nil {
		fmt.Println("Error setting up config:", err)
		return
	}

	if config.APIKey == "" {
		color.New(color.FgYellow).Print(welcomeMessage)
		fmt.Println("")
	}

	color.New(color.FgYellow).Println("Type .help for more information.")

	historyFile, err := getDataFile("cmd_history.txt")
	if err != nil {
		fmt.Println("Error creating history file:", err)
		return
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          ">>> ",
		HistoryFile:     historyFile,
		HistoryLimit:    config.CmdHistoryLimit,
		InterruptPrompt: "^C",
		EOFPrompt:       ".quit",
	})
	if err != nil {
		panic(err)
	}

	if err := trimHistoryFile(historyFile, config.CmdHistoryLimit); err != nil {
		log.Printf("Error trimming history at startup: %v", err)
	}

	defer func() {
		if err := trimHistoryFile(historyFile, config.CmdHistoryLimit); err != nil {
			log.Printf("Error trimming history on close: %v", err)
		}
		rl.Close()
	}()

	for {
		if currentDict != "" {
			color.New(color.FgYellow).Printf("%s >>> ", currentDict)
			yellow := "\033[33m"
			reset := "\033[0m"
			rl.SetPrompt(yellow + currentDict + " >>> " + reset)
		} else {
			fmt.Print(">>> ")
			rl.SetPrompt(">>> ")
		}
		input, err := rl.Readline()
		if err != nil {
			// Handle EOF gracefully
			if err.Error() == "EOF" {
				fmt.Println()
				break
			}
			fmt.Println("Error reading input:", err)
			return
		}

		input = strings.TrimSpace(input)
		parts := strings.Fields(input)
		if len(parts) == 0 {
			continue
		}

		command := parts[0]
		args := parts[1:]

		switch command {
		case ".quit":
			return
		case ".help":
			handleHelpCommand()
		case ".history":
			if err := handleHistoryCommand(); err != nil {
				color.New(color.FgRed, color.Bold).Println("Error:", err)
			}
		case ".dict":
			if err := handleDictCommand(args); err != nil {
				color.New(color.FgRed, color.Bold).Println("Error:", err)
			}
		case ".set":
			if err := handleSetCommand(args); err != nil {
				color.New(color.FgRed, color.Bold).Println("Error:", err)
			}
		default:
			if err := handleTranslation(command); err != nil {
				color.New(color.FgRed, color.Bold).Println("Error:", err)
			}
		}
	}
}

func trimHistoryFile(filename string, maxLines int) error {
	// Read the file
	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist, nothing to trim
		}
		return err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	// Trim if necessary
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	// Write back
	return os.WriteFile(filename, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func handleTranslation(word string) error {
	if currentDict == "" {
		return fmt.Errorf("no dictionary selected. Use .dict <key> to select one")
	}

	// Caching logic
	cacheKey := getTranslationCacheKey(word, currentDict)
	cacheFile, err := getCacheFile(cacheKey + ".json")
	if err != nil {
		return err
	}

	cacheTTL := time.Duration(config.CacheTTL) * time.Second
	if isCacheValid(cacheFile, cacheTTL) {
		file, err := os.Open(cacheFile)
		if err != nil {
			return fmt.Errorf("could not open cache file: %w", err)
		}
		defer file.Close()

		body, err := io.ReadAll(file)
		if err != nil {
			return fmt.Errorf("could not read cache file: %w", err)
		}

		var translations TranslationResponse
		if err := json.Unmarshal(body, &translations); err != nil {
			return fmt.Errorf("could not unmarshal cached json: %w", err)
		}
		displayTranslation(translations, currentDict)

		if err := addSearchHistory(word, currentDict); err != nil {
			// Log the error, but don't fail the command
			log.Printf("could not add search history: %v", err)
		}
		return nil
	}

	req, err := http.NewRequest("GET", dictionaryURL, nil)
	if err != nil {
		return fmt.Errorf("could not create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("q", word)
	q.Add("l", currentDict)
	req.URL.RawQuery = q.Encode()
	req.Header.Add("X-Secret", config.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not fetch translation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		fmt.Println("No translation found")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not read response body: %w", err)
	}

	// Write to cache
	if err := os.WriteFile(cacheFile, body, 0644); err != nil {
		// Log this error, but don't fail the command
		fmt.Printf("could not write cache file: %v", err)
	}

	var translations TranslationResponse
	if err := json.Unmarshal(body, &translations); err != nil {
		return fmt.Errorf("could not unmarshal json: %w", err)
	}

	displayTranslation(translations, currentDict)

	if err := addSearchHistory(word, currentDict); err != nil {
		// Log the error, but don't fail the command
		log.Printf("could not add search history: %v", err)
	}

	return nil
}

func addSearchHistory(term, dictionary string) error {
	stmt, err := db.Prepare("INSERT INTO search_history(searched_term, dict, date) VALUES(?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(term, dictionary, time.Now())
	return err
}

func getHalfWidth() int {
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 80 // Fallback to 80 columns if unknown
	}

	return termWidth / 2
}

func newTable() table.Writer {
	halfWidth := getHalfWidth()
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	// Force each column to take 50% of terminal width
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, WidthMax: halfWidth, WidthMin: halfWidth},
		{Number: 2, WidthMax: halfWidth, WidthMin: halfWidth},
	})
	// Set no-border style
	t.SetStyle(table.Style{
		Name:   "NoBorders",
		Box:    table.BoxStyle{},
		Color:  table.ColorOptions{},
		Format: table.FormatOptions{},
		Options: table.Options{
			DrawBorder:      false,
			SeparateColumns: false,
			SeparateHeader:  false,
			SeparateFooter:  false,
		},
	})
	return t
}

func displayTranslation(translations TranslationResponse, dictKey string) {

	for _, lang := range translations {
		color.New(color.FgRed, color.Bold).Printf("\n%s > %s\n", strings.ToUpper(lang.Lang), strings.ToUpper(strings.Replace(dictKey, lang.Lang, "", 1)))
		for _, hit := range lang.Hits {
			if len(hit.Roms) > 0 {
				for i, rom := range hit.Roms {
					color.New(color.FgYellow, color.Bold).Printf("\n%s. %s\n", toRoman(i+1), rom.Headword)
					for _, arab := range rom.Arabs {
						color.New(color.FgGreen).Println(parseHTML(arab.Header))
						t := newTable()
						for _, translation := range arab.Translations {
							t.AppendRow(table.Row{parseHTML(translation.Source), parseHTML(translation.Target)})
						}
						t.Render()
					}
				}
			} else {
				t := newTable()
				t.AppendRow(table.Row{parseHTML(hit.Source), parseHTML(hit.Target)})
				t.Render()
			}
		}
	}
	fmt.Println()
}

func toRoman(num int) string {
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	romans := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for num >= v {
			num -= v
			sb.WriteString(romans[i])
		}
	}
	return sb.String()
}

func parseHTML(htmlString string) string {
	doc, err := html.Parse(strings.NewReader(htmlString))
	if err != nil {
		return htmlString // return raw string on error
	}
	var f func(*html.Node)
	var sb strings.Builder
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)
	return sb.String()
}

func getTranslationCacheKey(word, dict string) string {
	hash := sha256.Sum256([]byte(word + "_" + dict))
	return hex.EncodeToString(hash[:])
}

func handleHelpCommand() {
	color.New(color.FgYellow).Println("Available commands:")
	fmt.Println(".help - Show this help message")
	fmt.Println(".quit - Exit the program")
	fmt.Println(".dict - List available dictionaries")
	fmt.Println(".dict <key> - Set the current dictionary")
	fmt.Println(".history - Show search history")
	fmt.Println(".set - Show current settings")
	fmt.Println(".set <var> <value> - Set a configuration variable")
}

func handleHistoryCommand() error {
	rows, err := db.Query("SELECT searched_term, dict, date FROM search_history ORDER BY date DESC")
	if err != nil {
		return fmt.Errorf("could not query search history: %w", err)
	}
	defer rows.Close()

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Searched Term", "Dictionary", "Date"})

	for rows.Next() {
		var term, dict string
		var date time.Time
		if err := rows.Scan(&term, &dict, &date); err != nil {
			return fmt.Errorf("could not scan row: %w", err)
		}
		t.AppendRow(table.Row{term, dict, date.Format("2006-01-02 15:04:05")})
	}

	t.Render()
	return nil
}

func handleSetCommand(args []string) error {
	if len(args) == 0 {
		color.New(color.FgYellow).Println("Usage: .set <variable> <value>")
		color.New(color.FgGreen).Printf("api_key")
		fmt.Printf(": %s\n", config.APIKey)
		color.New(color.FgGreen).Printf("cache_ttl")
		fmt.Printf(": %d\n", config.CacheTTL)
		color.New(color.FgGreen).Printf("cmd_history_limit")
		fmt.Printf(": %d\n", config.CmdHistoryLimit)
		return nil
	}

	if len(args) != 2 {
		return fmt.Errorf("invalid number of arguments")
	}

	varName := args[0]
	varValue := args[1]

	switch varName {
	case "api_key":
		config.APIKey = varValue
	case "cache_ttl":
		val, err := strconv.Atoi(varValue)
		if err != nil {
			return fmt.Errorf("invalid value for cache_ttl: %s", varValue)
		}
		config.CacheTTL = val
	case "cmd_history_limit":
		val, err := strconv.Atoi(varValue)
		if err != nil {
			return fmt.Errorf("invalid value for cmd_history_limit: %s", varValue)
		}
		config.CmdHistoryLimit = val
	default:
		return fmt.Errorf("unknown variable: %s", varName)
	}

	return writeConfig()
}

func writeConfig() error {
	appConfigDir := filepath.Join(xdg.ConfigHome, "pons-cli")
	configFile := filepath.Join(appConfigDir, "config.toml")

	file, err := os.Create(configFile)
	if err != nil {
		return fmt.Errorf("could not create config file: %w", err)
	}
	defer file.Close()

	if err := toml.NewEncoder(file).Encode(config); err != nil {
		return fmt.Errorf("could not encode config to file: %w", err)
	}

	return nil
}

func handleDictCommand(args []string) error {
	dictionaries, err := getDictionaries()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		color.New(color.FgYellow).Println("Usage: .dict <dictionary_key>")
		for _, dict := range dictionaries {
			if len(dict.Languages) == 2 {
				color.New(color.FgGreen).Printf("%s", dict.Key)
				fmt.Printf(": %s\n", dict.SimpleLabel)
			}
		}
		return nil
	}

	dictKey := args[0]
	for _, dict := range dictionaries {
		if dict.Key == dictKey {
			currentDict = dictKey
			return nil
		}
	}

	return fmt.Errorf("unknown dictionary key: %s", dictKey)
}

func getDictionaries() ([]Dictionary, error) {
	cacheFile, err := getCacheFile("dictionaries.json")
	if err != nil {
		return nil, err
	}

	cacheTTL := time.Duration(config.CacheTTL) * time.Second
	if isCacheValid(cacheFile, cacheTTL) {
		file, err := os.Open(cacheFile)
		if err != nil {
			return nil, fmt.Errorf("could not open cache file: %w", err)
		}
		defer file.Close()

		body, err := io.ReadAll(file)
		if err != nil {
			return nil, fmt.Errorf("could not read cache file: %w", err)
		}

		var dictionaries []Dictionary
		if err := json.Unmarshal(body, &dictionaries); err != nil {
			return nil, fmt.Errorf("could not unmarshal cached json: %w", err)
		}
		//fmt.Println("from cache")
		return dictionaries, nil
	}

	// Cache is not valid, fetch from API
	req, err := http.NewRequest("GET", dictionariesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("language", "en")
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch dictionaries: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read response body: %w", err)
	}

	// Write to cache
	if err := os.WriteFile(cacheFile, body, 0644); err != nil {
		// Log this error, but don't fail the command
		fmt.Printf("could not write cache file: %v", err)
	}

	var dictionaries []Dictionary
	if err := json.Unmarshal(body, &dictionaries); err != nil {
		return nil, fmt.Errorf("could not unmarshal json: %w", err)
	}

	return dictionaries, nil
}

func getCacheFile(name string) (string, error) {
	appCacheDir := filepath.Join(xdg.CacheHome, "pons-cli")
	return filepath.Join(appCacheDir, name), nil
}

func isCacheValid(path string, ttl time.Duration) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < ttl
}

func getDataFile(name string) (string, error) {
	appDataDir := filepath.Join(xdg.DataHome, "pons-cli")
	return filepath.Join(appDataDir, name), nil
}

func setup() error {
	if err := setupConfig(); err != nil {
		return err
	}
	if err := setupCache(); err != nil {
		return err
	}
	if err := setupDataDir(); err != nil {
		return err
	}
	if err := setupDatabase(); err != nil {
		return err
	}
	return nil
}

func setupDatabase() error {
	dbFile, err := getDataFile("pons-cli.db")
	if err != nil {
		return fmt.Errorf("could not get db file path: %w", err)
	}

	db, err = sql.Open("sqlite3", dbFile)
	if err != nil {
		return fmt.Errorf("could not open database: %w", err)
	}

	// Create table if not exists
	statement, err := db.Prepare(`
		CREATE TABLE IF NOT EXISTS search_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			searched_term TEXT NOT NULL,
			dict TEXT NOT NULL,
			date DATETIME NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("could not prepare statement: %w", err)
	}
	_, err = statement.Exec()
	if err != nil {
		return fmt.Errorf("could not execute statement: %w", err)
	}

	return nil
}

func setupDataDir() error {
	appDataDir := filepath.Join(xdg.DataHome, "pons-cli")
	if err := os.MkdirAll(appDataDir, 0755); err != nil {
		return fmt.Errorf("could not create app data dir: %w", err)
	}

	return nil
}

func setupCache() error {
	appCacheDir := filepath.Join(xdg.CacheHome, "pons-cli")
	if err := os.MkdirAll(appCacheDir, 0755); err != nil {
		return fmt.Errorf("could not create app cache dir: %w", err)
	}

	if err := cleanupExpiredCacheFiles(); err != nil {
		log.Printf("Error cleaning up expired cache files: %v", err)
	}

	return nil
}

func cleanupExpiredCacheFiles() error {
	appCacheDir := filepath.Join(xdg.CacheHome, "pons-cli")
	files, err := os.ReadDir(appCacheDir)
	if err != nil {
		return fmt.Errorf("could not read cache directory: %w", err)
	}

	cacheTTL := time.Duration(config.CacheTTL) * time.Second

	for _, file := range files {
		if !file.IsDir() {
			filePath := filepath.Join(appCacheDir, file.Name())
			info, err := file.Info()
			if err != nil {
				log.Printf("could not get file info for %s: %v", filePath, err)
				continue
			}
			if time.Since(info.ModTime()) > cacheTTL {
				err := os.Remove(filePath)
				if err != nil {
					log.Printf("could not remove expired cache file %s: %v", filePath, err)
				}
			}
		}
	}
	return nil
}

func setupConfig() error {
	const defaultApiKey = ""
	const defaultCacheTTL = 604800 // 7 days
	const defaultCmdHistoryLimit = 100

	appConfigDir := filepath.Join(xdg.ConfigHome, "pons-cli")
	if err := os.MkdirAll(appConfigDir, 0755); err != nil {
		return fmt.Errorf("could not create app config dir: %w", err)
	}

	configFile := filepath.Join(appConfigDir, "config.toml")

	md, err := toml.DecodeFile(configFile, &config)

	needsWrite := false
	if os.IsNotExist(err) {
		config.APIKey = defaultApiKey
		config.CacheTTL = defaultCacheTTL
		config.CmdHistoryLimit = defaultCmdHistoryLimit
		needsWrite = true
	} else if err != nil {
		return fmt.Errorf("could not decode config file: %w", err)
	}

	if !md.IsDefined("api_key") {
		config.APIKey = defaultApiKey
		needsWrite = true
	}

	if !md.IsDefined("cache_ttl") {
		config.CacheTTL = defaultCacheTTL
		needsWrite = true
	}

	if !md.IsDefined("cmd_history_limit") {
		config.CmdHistoryLimit = defaultCmdHistoryLimit
		needsWrite = true
	}

	if needsWrite {
		return writeConfig()
	}

	return nil
}
