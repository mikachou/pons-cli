package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"golang.org/x/net/html"
	"golang.org/x/term"
)

const baseURL = "https://api.pons.com/v1/"

const dictionaryURL = baseURL + "dictionary"
const dictionariesURL = baseURL + "dictionaries"

type Config struct {
	APIKey   string `toml:"api_key"`
	CacheTTL int    `toml:"cache_ttl"`
}

var config Config
var currentDict string

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

func main() {
	if err := setup(); err != nil {
		fmt.Println("Error setting up config:", err)
		return
	}

	color.New(color.FgYellow).Println("Type .help for more information.")

	reader := bufio.NewReader(os.Stdin)
	for {
		if currentDict != "" {
			color.New(color.FgYellow).Printf("%s >>> ", currentDict)
		} else {
			fmt.Print(">>> ")
		}
		input, err := reader.ReadString('\n')
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

	return nil
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
	fmt.Println(".set - Show current settings")
	fmt.Println(".set <var> <value> - Set a configuration variable")
}

func handleSetCommand(args []string) error {
	if len(args) == 0 {
		color.New(color.FgYellow).Println("Usage: .set <variable> <value>")
		color.New(color.FgGreen).Printf("api_key")
		fmt.Printf(": %s\n", config.APIKey)
		color.New(color.FgGreen).Printf("cache_ttl")
		fmt.Printf(": %d\n", config.CacheTTL)
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
	default:
		return fmt.Errorf("unknown variable: %s", varName)
	}

	return writeConfig()
}

func writeConfig() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("could not get user config dir: %w", err)
	}

	appConfigDir := filepath.Join(configDir, "pons-cli")
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
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not get user cache dir: %w", err)
	}
	appCacheDir := filepath.Join(cacheDir, "pons-cli")
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

func setup() error {
	if err := setupConfig(); err != nil {
		return err
	}
	if err := setupCache(); err != nil {
		return err
	}
	return nil
}

func setupCache() error {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("could not get user cache dir: %w", err)
	}

	appCacheDir := filepath.Join(cacheDir, "pons-cli")
	if err := os.MkdirAll(appCacheDir, 0755); err != nil {
		return fmt.Errorf("could not create app cache dir: %w", err)
	}

	return nil
}

func setupConfig() error {
	const defaultApiKey = ""
	const defaultCacheTTL = 604800 // 7 days

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("could not get user config dir: %w", err)
	}

	appCacheDir := filepath.Join(configDir, "pons-cli")
	if err := os.MkdirAll(appCacheDir, 0755); err != nil {
		return fmt.Errorf("could not create app config dir: %w", err)
	}

	configFile := filepath.Join(appCacheDir, "config.toml")

	md, err := toml.DecodeFile(configFile, &config)

	needsWrite := false
	if os.IsNotExist(err) {
		config.APIKey = defaultApiKey
		config.CacheTTL = defaultCacheTTL
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

	if needsWrite {
		return writeConfig()
	}

	return nil
}
