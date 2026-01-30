package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

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

func readInput() ([]byte, error) {
	if len(os.Args) > 1 {
		return os.ReadFile(os.Args[1])
	}
	return io.ReadAll(os.Stdin)
}

func runWithGoModule(code []byte) error {
	tmpDir, err := os.MkdirTemp("", "gogo-*")
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	defer os.RemoveAll(tmpDir)

	// Определяем версию Go для go.mod
	// runtime.Version() возвращает "go1.21.5", нам нужна "1.21"
	goVersion := runtime.Version()
	goVersion = strings.TrimPrefix(goVersion, "go")
	parts := strings.Split(goVersion, ".")
	if len(parts) >= 2 {
		goVersion = parts[0] + "." + parts[1]
	}

	// Создаём go.mod
	modContent := fmt.Sprintf("module gorun-main\n\ngo %s\n", goVersion)
	modPath := filepath.Join(tmpDir, "go.mod")
	if err = os.WriteFile(modPath, []byte(modContent), 0644); err != nil {
		return fmt.Errorf("failed to create go.mod: %w", err)
	}

	mainPath := filepath.Join(tmpDir, "main.go")
	if err = os.WriteFile(mainPath, code, 0644); err != nil {
		return fmt.Errorf("failed to write in main.go: %w", err)
	}

	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = tmpDir
	tidyCmd.Stderr = os.Stderr
	tidyCmd.Stdout = os.Stdout
	
	fmt.Fprintln(os.Stderr, "📦 Подготовка модулей...")

	if err := tidyCmd.Run(); err != nil {
		return fmt.Errorf("failed to tidy modules: %w", err)
	}

	cmd := exec.Command("go", "run", ".") // "-mod=mod" но без контрольных сумм
	cmd.Dir = tmpDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	fmt.Fprintln(os.Stderr, "Компиляция и запуск...")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}

	return nil
}

func main() {
	code, err := readInput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка чтения: %v\n", err)
		os.Exit(1)
	}
    
    if len(code) == 0 {
        fmt.Println("Использование: echo 'код' | gorun")
        fmt.Println("Пример: echo 'package main; import \"fmt\"; func main() { fmt.Println(\"Hello\") }' | gorun")
        return
    }
    
	if err = validateCode(code); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка валидации: %v\n", err)
		os.Exit(1)
	}

	if code, err = stripShebang(code); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

    // Выводим что получили (для отладки)
    fmt.Printf("Получен код (%d байт):\n", len(code))
    fmt.Println("────────────────────")
    fmt.Println(string(code))

	if err := runWithGoModule(code); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// или убрать это потом или всех их послать в stderr что выше
	// Всё, что пишет твоя утилита (диагностика, прогресс, ошибки) → stderr. 
	// Только вывод пользовательского кода → stdout.
	fmt.Fprintln(os.Stderr, "\nВыполнено успешно!")
}