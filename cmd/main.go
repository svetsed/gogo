package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
)

var (
	noCache     = flag.Bool("once", false, "Disable cache (compile and run without saving)")
	clearCache  = flag.Bool("clear", false, "Remove all cached binaries")
	showVersion = flag.Bool("ver", false, "Show version")

	errTitle    = color.New(color.FgRed, color.Bold, color.Underline)
	errFile     = color.New(color.FgCyan)
	errLine     = color.New(color.FgYellow, color.Bold)
	errMsg      = color.New(color.FgRed)
	errHint     = color.New(color.FgHiBlack)
	codeLine    = color.New(color.FgWhite)
	codeNum     = color.New(color.FgHiBlack)

	// Паттерн ошибки Go: файл:строка:колонка: сообщение
	errorPattern = regexp.MustCompile(`^(.+)\:(\d+)\:(\d+)\:\s*(.+)$`)
)

func printErrWithColor(c *color.Color, format string, args ...any) {
	c.Fprintf(os.Stderr, format+"\n", args...)
}

func parseGoErrors(rawOutput string, sourceCode []byte) {
	lines := strings.Split(rawOutput, "\n")
	foundErrors := false
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := errorPattern.FindStringSubmatch(line)
		if len(matches) != 5 {
			// Не распарсилось — выводим как есть
			fmt.Println(line)
			continue
		}

		foundErrors = true
		file := matches[1]
		lineNum, _ := strconv.Atoi(matches[2])
		col := matches[3]
		message := matches[4]

		errType, description := splitErrorType(message)

		errTitle.Printf("%s\n", errType)
		errFile.Printf("   %s", file)
		errLine.Printf(":%d", lineNum)
		errHint.Printf(":%s\n", col)
		errMsg.Printf("   %s\n", description)

		if len(sourceCode) > 0 {
			showSourceContext(sourceCode, lineNum)
		}

		fmt.Println()	
	}

	if foundErrors {
		color.Red("\nCompilation failed")
	}
}

func splitErrorType(msg string) (typ, desc string) {
	if  idx := strings.Index(msg, ":"); idx > 0 {
		possibleType := msg[:idx]
		switch possibleType {
		case "undefined", "cannot find package", "imported and not used",
			"declared but not used", "syntax error", "invalid operation":
			return possibleType, strings.TrimSpace(msg[idx+1:])
		}
	}
	return "error", msg
}

func showSourceContext(source []byte, errorLine int) {
	lines := strings.Split(string(source), "\n")
	if errorLine < 1 || errorLine > len(lines) {
		return
	}

	start := errorLine - 2
	if start < 0 {
		start = 0
	}

	end := errorLine + 1
	if end > len(lines) {
		end = len(lines)
	}

	for i := start; i < end; i++ {
		num := i + 1
		if num == errorLine {
			codeNum.Printf("  → %d | ", num)
			codeLine.Println(lines[i])
		} else {
			codeNum.Printf("    %d | ", num)
			codeLine.Println(lines[i])
		}
	}
}

func buildWith(tmpDir, binaryPath string, sourceCode []byte) error {
	var stderr bytes.Buffer

	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = tmpDir
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		output := stderr.String()

		if output == "" {
			return fmt.Errorf("build failed: %v", err)
		}

		parseGoErrors(output, sourceCode)
		return fmt.Errorf("compilation failed")

	}

	return nil
}

func getCachePaths(code []byte) (dir, binaryPath string) {
	hash := sha256.Sum256(code)
	hashStr := hex.EncodeToString(hash[:])[:16]

	// ~/.cache/gogo/ на Unix, %LOCALAPPDATA%\gogo\ на Windows
	casheRoot, err := os.UserCacheDir() // error
	if err != nil {
		return "", ""
	}
	dir = filepath.Join(casheRoot, "gogo", hashStr)

	binName := "run"
	if runtime.GOOS == "windows" {
		binName = "run.exe"
	}

	binaryPath = filepath.Join(dir, binName)

	return 
}

func validateCode(code []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(code))
	for scanner.Scan() {
		line := strings.TrimSpace((scanner.Text()))
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		} 
		
		if strings.HasPrefix(line, "package ") {
			return nil
		}
		break
	}

	return fmt.Errorf("code must contain \"package <name>\"")
}

func stripShebang(code []byte) ([]byte, error) {
	if len(code) > 2 && code[0] == '#' && code[1] == '!' {
		for i := 2; i < len(code); i++ {
			if code[i] == '\n' {
				return code[i+1:], nil
			}
		}
		return code, fmt.Errorf("incorrect data received")
	}

	return code, nil
}

func readInput(filename string) ([]byte, error) {
	if filename != "" {
		return os.ReadFile(filename)
	}
	return io.ReadAll(os.Stdin)
}

func clearAllCache() error {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return err
	}

	gogoCache := filepath.Join(cacheRoot, "gogo")
	return os.RemoveAll(gogoCache)
}

// createModule создаёт go.mod и main.go во временной директории.
func createModule(dir string, code []byte) error {
	// Версия Go
	goVersion := runtime.Version()
	goVersion = strings.TrimPrefix(goVersion, "go")
	parts := strings.Split(goVersion, ".")
	if len(parts) >= 2 {
		goVersion = parts[0] + "." + parts[1]
	}

	// go.mod
	modContent := fmt.Sprintf("module gogo-main\n\ngo %s\n", goVersion)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0644); err != nil {
		return err
	}

	// main.go
	return os.WriteFile(filepath.Join(dir, "main.go"), code, 0644)
}

// runCached: с кешем (по умолчанию).
func runCached(code []byte) error {
	cacheDir, binaryPath := getCachePaths(code)

	if info, err := os.Stat(binaryPath); err == nil {
		if time.Since(info.ModTime()) < 3*24*time.Hour {
			color.Yellow("Using cached binary")
			return runBinary(binaryPath)
		}
	}

	// Создаём директорию кеша
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	// Сохраняем исходник для отладки
	sourcePath := filepath.Join(cacheDir, "main.go")
	err := os.WriteFile(sourcePath, code, 0644)
	if err != nil {
		return err
	}

	// Компиляция во временной папке
	tmpDir, err := os.MkdirTemp("", "gogo-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir) // error

	if err := createModule(tmpDir, code); err != nil {
		return err
	}

	// go mod tidy
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	color.Yellow("First run: downloading dependencies...")
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	color.Yellow("Building...")

if err := buildWith(tmpDir, binaryPath, code); err != nil {
    os.RemoveAll(cacheDir)
    return err
}

	if runtime.GOOS != "windows" {
		os.Chmod(binaryPath, 0755) //error
	}

	meta := fmt.Sprintf("Built: %s\nGo: %s\n", 
        time.Now().Format(time.RFC3339), 
        runtime.Version())
	
    os.WriteFile(filepath.Join(cacheDir, "meta.txt"), []byte(meta), 0644) //error

	return runBinary(binaryPath)
}

// runOnce: без кеша (для -once флага).
func runOnce(code []byte) error {
	tmpDir, err := os.MkdirTemp("", "gogo-once-*")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tmpDir) // error

	if err := createModule(tmpDir, code); err != nil {
		return err
	}

	// go mod tidy
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	fmt.Fprintln(os.Stderr, "Checking dependencies...")
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Running without cache...")

	// go run
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = tmpDir
	cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    cmd.Stdin = os.Stdin

	return cmd.Run()
}

func runBinary(path string) error {
	cmd := exec.Command(path)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    cmd.Stdin = os.Stdin
    return cmd.Run()
}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Println("gogo v0.1.0")
		return
	}

	if *clearCache {
		if err := clearAllCache(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to clear cache: %v\n", err)
            os.Exit(1)
		}
		fmt.Println("Cache cleared!")
        return
	}

	code, err := readInput(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
		os.Exit(1)
	}

	if *noCache {
		err = runOnce(code)
	} else {
		err = runCached(code)
	}
    
	if err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }

    if len(code) == 0 {
        fmt.Fprintln(os.Stderr, "Usage: echo 'code' | gogo")
		fmt.Fprintln(os.Stderr, "       gogo file.go")
		fmt.Fprintln(os.Stderr, "Flags: -once (no cache), -clear (clean cache), -version")
		os.Exit(1)
        return
    }
    
	if err = validateCode(code); err != nil {
		fmt.Fprintf(os.Stderr, "Error validation: %v\n", err)
		os.Exit(1)
	}

	if code, err = stripShebang(code); err != nil {
		fmt.Fprintf(os.Stderr, "Error shebang: %v\n", err)
		os.Exit(1)
	}

	if *noCache {
		err = runOnce(code)
	} else {
		err = runCached(code)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}