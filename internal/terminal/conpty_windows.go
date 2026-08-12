//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsPTY struct {
	input       *os.File
	output      *os.File
	pseudo      windows.Handle
	process     windows.Handle
	pid         uint32
	ptyInput    windows.Handle
	ptyOutput   windows.Handle
	closeIOOnce sync.Once
	closeOnce   sync.Once
}

func startWindowsPTY(spec Spec, width, height int) (*windowsPTY, error) {
	var ptyInput, inputWrite, outputRead, ptyOutput windows.Handle
	if err := windows.CreatePipe(&ptyInput, &inputWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("create ConPTY input pipe: %w", err)
	}
	closeHandles := func() {
		for _, handle := range []windows.Handle{ptyInput, inputWrite, outputRead, ptyOutput} {
			if handle != 0 && handle != windows.InvalidHandle {
				_ = windows.CloseHandle(handle)
			}
		}
	}
	if err := windows.CreatePipe(&outputRead, &ptyOutput, nil, 0); err != nil {
		closeHandles()
		return nil, fmt.Errorf("create ConPTY output pipe: %w", err)
	}
	// Only the pseudo console owns its ends of these pipes; the child receives
	// the HPCON through its process attribute list instead of inheriting handles.
	_ = windows.SetHandleInformation(inputWrite, windows.HANDLE_FLAG_INHERIT, 0)
	_ = windows.SetHandleInformation(outputRead, windows.HANDLE_FLAG_INHERIT, 0)

	var pseudo windows.Handle
	coordinate := windows.Coord{X: int16(width), Y: int16(height)}
	if err := windows.CreatePseudoConsole(coordinate, ptyInput, ptyOutput, 0, &pseudo); err != nil {
		closeHandles()
		return nil, fmt.Errorf("create ConPTY: %w", err)
	}

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		closeHandles()
		return nil, fmt.Errorf("allocate ConPTY process attributes: %w", err)
	}
	defer attributes.Delete()
	pseudoAttribute := new(windows.Handle)
	*pseudoAttribute = pseudo
	if err := attributes.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		unsafe.Pointer(pseudoAttribute),
		unsafe.Sizeof(*pseudoAttribute),
	); err != nil {
		windows.ClosePseudoConsole(pseudo)
		closeHandles()
		return nil, fmt.Errorf("attach ConPTY process attribute: %w", err)
	}

	arguments := append([]string{spec.Path}, spec.Args...)
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(arguments))
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		closeHandles()
		return nil, fmt.Errorf("encode ConPTY command line: %w", err)
	}
	application, err := windows.UTF16PtrFromString(spec.Path)
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		closeHandles()
		return nil, fmt.Errorf("encode ConPTY executable: %w", err)
	}
	var directory *uint16
	if spec.Dir != "" {
		directory, err = windows.UTF16PtrFromString(spec.Dir)
		if err != nil {
			windows.ClosePseudoConsole(pseudo)
			closeHandles()
			return nil, fmt.Errorf("encode ConPTY working directory: %w", err)
		}
	}
	environment := spec.Env
	if environment == nil {
		environment = os.Environ()
	}
	environmentBlock, err := windowsEnvironmentBlock(environment)
	if err != nil {
		windows.ClosePseudoConsole(pseudo)
		closeHandles()
		return nil, err
	}

	startup := windows.StartupInfoEx{}
	startup.StartupInfo.Cb = uint32(unsafe.Sizeof(startup))
	startup.ProcThreadAttributeList = attributes.List()
	process := windows.ProcessInformation{}
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(
		application,
		&commandLine[0],
		nil,
		nil,
		false,
		flags,
		&environmentBlock[0],
		directory,
		&startup.StartupInfo,
		&process,
	); err != nil {
		windows.ClosePseudoConsole(pseudo)
		closeHandles()
		return nil, fmt.Errorf("start ConPTY child: %w", err)
	}
	runtime.KeepAlive(pseudoAttribute)
	_ = windows.CloseHandle(process.Thread)

	return &windowsPTY{
		input:     os.NewFile(uintptr(inputWrite), "lisan-conpty-input"),
		output:    os.NewFile(uintptr(outputRead), "lisan-conpty-output"),
		pseudo:    pseudo,
		process:   process.Process,
		pid:       process.ProcessId,
		ptyInput:  ptyInput,
		ptyOutput: ptyOutput,
	}, nil
}

func windowsEnvironmentBlock(environment []string) ([]uint16, error) {
	environment = slices.Clone(environment)
	slices.SortFunc(environment, func(left, right string) int {
		leftKey, _, _ := strings.Cut(left, "=")
		rightKey, _, _ := strings.Cut(right, "=")
		return strings.Compare(strings.ToUpper(leftKey), strings.ToUpper(rightKey))
	})
	block := make([]uint16, 0, len(environment)*16+2)
	for _, item := range environment {
		if strings.IndexByte(item, 0) >= 0 {
			return nil, errors.New("terminal environment contains NUL")
		}
		for _, value := range item {
			block = utf16.AppendRune(block, value)
		}
		block = append(block, 0)
	}
	block = append(block, 0)
	if len(environment) == 0 {
		block = append(block, 0)
	}
	return block, nil
}

func (p *windowsPTY) Read(data []byte) (int, error)  { return p.output.Read(data) }
func (p *windowsPTY) Write(data []byte) (int, error) { return p.input.Write(data) }

func (p *windowsPTY) Resize(width, height int) error {
	return windows.ResizePseudoConsole(p.pseudo, windows.Coord{X: int16(width), Y: int16(height)})
}

func (p *windowsPTY) Wait() (uint32, error) {
	result, err := windows.WaitForSingleObject(p.process, windows.INFINITE)
	if err != nil {
		return 0, err
	}
	if result != windows.WAIT_OBJECT_0 {
		return 0, fmt.Errorf("unexpected ConPTY wait result: %d", result)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
		return 0, err
	}
	return code, nil
}

func (p *windowsPTY) terminate() {
	_ = windows.TerminateProcess(p.process, 1)
}

// closeIO releases the pseudo console and both directions of its transport.
// The process handle remains valid so a concurrent waiter can collect status.
func (p *windowsPTY) closeIO() {
	p.closeIOOnce.Do(func() {
		_ = p.input.Close()
		_ = p.output.Close()
		windows.ClosePseudoConsole(p.pseudo)
		_ = windows.CloseHandle(p.ptyInput)
		_ = windows.CloseHandle(p.ptyOutput)
	})
}

func (p *windowsPTY) closeProcess() {
	p.closeOnce.Do(func() {
		_ = windows.CloseHandle(p.process)
	})
}
