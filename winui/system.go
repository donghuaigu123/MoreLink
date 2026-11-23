//go:build windows

package winui

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	pOpenClipboard       = user32.NewProc("OpenClipboard")
	pCloseClipboard      = user32.NewProc("CloseClipboard")
	pEmptyClipboard      = user32.NewProc("EmptyClipboard")
	pSetClipboardData    = user32.NewProc("SetClipboardData")
	pGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	pGlobalLock          = kernel32.NewProc("GlobalLock")
	pGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	pCopyMemory          = kernel32.NewProc("RtlMoveMemory")
	pRegOpenKeyEx        = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueEx       = advapi32.NewProc("RegSetValueExW")
	pRegQueryValue       = advapi32.NewProc("RegQueryValueExW")
	pRegDeleteValue      = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey         = advapi32.NewProc("RegCloseKey")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenu          = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pLoadIcon            = user32.NewProc("LoadIconW")
	pSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	pCoTaskMemFree       = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")
)

// ---- System tray ----

type NOTIFYICONDATAA struct {
	CbSize           uint32
	Hwnd             HWND
	UID              uint32
	Flags            uint32
	CallbackMessage  uint32
	HIcon            syscall.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	Timeout          uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         [16]byte
	BalloonIcon      syscall.Handle
}

const (
	NIM_ADD    = 0
	NIM_MODIFY = 1
	NIM_DELETE = 2
	NIF_MESSAGE = 0x01
	NIF_ICON    = 0x02
	NIF_TIP     = 0x04

	MF_STRING     = 0x0000
	MF_SEPARATOR  = 0x0800
	MF_CHECKED    = 0x0008
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	IDI_APPLICATION = 32512
)

var trayIconHandle syscall.Handle

func notifyIcon(hwnd HWND, action uint32, tip string) bool {
	hi, _, _ := pLoadIcon.Call(0, IDI_APPLICATION)
	trayIconHandle = syscall.Handle(hi)
	n := NOTIFYICONDATAA{
		CbSize:          uint32(unsafe.Sizeof(NOTIFYICONDATAA{})),
		Hwnd:            hwnd,
		UID:             1,
		Flags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		CallbackMessage: WM_TRAYICON,
		HIcon:           trayIconHandle,
	}
	copy(n.Tip[:], utf16Slice(tip))
	ret, _, _ := pShellNotifyIcon.Call(uintptr(action), uintptr(unsafe.Pointer(&n)))
	return ret != 0
}

func utf16Slice(s string) []uint16 {
	p, _ := syscall.UTF16FromString(s)
	return p
}

// AddTrayIcon installs a tray icon for the window.
func AddTrayIcon(hwnd HWND, tip string) { notifyIcon(hwnd, NIM_ADD, tip) }

// UpdateTrayIcon updates the tray icon tooltip.
func UpdateTrayIcon(hwnd HWND, tip string) { notifyIcon(hwnd, NIM_MODIFY, tip) }

// RemoveTrayIcon removes the tray icon (called on WM_DESTROY).
func RemoveTrayIcon(hwnd HWND) { notifyIcon(hwnd, NIM_DELETE, "") }

// ShowWindow shows/restores a window (SW_SHOW=5, SW_RESTORE=9).
func ShowWindow(hwnd HWND) { pShowWindow.Call(uintptr(hwnd), 9) }

// HideWindow hides a window into the tray.
func HideWindow(hwnd HWND) { pShowWindow.Call(uintptr(hwnd), SW_HIDE) }

// TrayMenuItem is one entry in the tray context menu.
type TrayMenuItem struct {
	ID      uintptr
	Text    string
	Checked bool
	Sep     bool
}

// TrackPopupMenu shows a popup menu at the cursor and returns the chosen ID.
func TrackPopupMenu(hwnd HWND, items []TrayMenuItem) uintptr {
	hMenu, _, _ := pCreatePopupMenu.Call()
	if hMenu == 0 {
		return 0
	}
	for _, it := range items {
		if it.Sep {
			pAppendMenu.Call(hMenu, MF_SEPARATOR, 0, 0)
			continue
		}
		flags := uintptr(MF_STRING)
		if it.Checked {
			flags |= MF_CHECKED
		}
		pAppendMenu.Call(hMenu, flags, it.ID, uintptr(unsafe.Pointer(utf16(it.Text))))
	}
	var pt struct{ X, Y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(uintptr(hwnd))
	sel, _, _ := pTrackPopupMenu.Call(hMenu, TPM_RIGHTBUTTON|TPM_RETURNCMD,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
	pDestroyMenu.Call(hMenu)
	return sel
}

// ---- Auto-start (registry Run key, per-user, no admin needed) ----

const (
	HKEY_CURRENT_USER = 0x80000001
	KEY_READ          = 0x20019
	KEY_WRITE         = 0x20006
	REG_SZ            = 1
)

// SetAutoStart adds or removes this executable from the per-user startup.
func SetAutoStart(appName string, enable bool) error {
	const subKey = "Software\\Microsoft\\Windows\\CurrentVersion\\Run"
	var hKey syscall.Handle
	ret, _, err := pRegOpenKeyEx.Call(HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(utf16(subKey))), 0, KEY_WRITE, uintptr(unsafe.Pointer(&hKey)))
	if ret != 0 {
		return err
	}
	defer pRegCloseKey.Call(uintptr(hKey))

	if !enable {
		pRegDeleteValue.Call(uintptr(hKey), uintptr(unsafe.Pointer(utf16(appName))))
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	cmd := `"` + exe + `"`
	data := utf16Slice(cmd)
	ret, _, err = pRegSetValueEx.Call(uintptr(hKey), uintptr(unsafe.Pointer(utf16(appName))),
		0, REG_SZ, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if ret != 0 {
		return err
	}
	return nil
}

// IsAutoStart checks whether the given app is set to run at startup.
func IsAutoStart(appName string) bool {
	const subKey = "Software\\Microsoft\\Windows\\CurrentVersion\\Run"
	var hKey syscall.Handle
	ret, _, _ := pRegOpenKeyEx.Call(HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(utf16(subKey))), 0, KEY_READ, uintptr(unsafe.Pointer(&hKey)))
	if ret != 0 {
		return false
	}
	defer pRegCloseKey.Call(uintptr(hKey))
	buf := make([]uint16, 1024)
	n := uint32(len(buf) * 2)
	ret, _, _ = pRegQueryValue.Call(uintptr(hKey), uintptr(unsafe.Pointer(utf16(appName))),
		0, uintptr(0), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	return ret == 0
}

// ---- Folder picker ----

type BROWSEINFO struct {
	HwndOwner      HWND
	PidlRoot       uintptr
	DisplayName    *uint16
	Title          *uint16
	Flags          uint32
	Callback       uintptr
	LParam         uintptr
	Image          int32
}

const BIF_RETURNONLYFSDIRS = 0x00000001

// SelectFolder opens a folder picker; returns "" if cancelled.
func SelectFolder(owner HWND, title string) string {
	display := make([]uint16, 260)
	bi := BROWSEINFO{
		HwndOwner:   owner,
		DisplayName: &display[0],
		Title:       utf16(title),
		Flags:       BIF_RETURNONLYFSDIRS,
	}
	pidl, _, _ := pSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer pCoTaskMemFree.Call(pidl)
	pathBuf := make([]uint16, 1024)
	if ok, _, _ := pSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0]))); ok != 0 {
		return syscall.UTF16ToString(pathBuf)
	}
	return ""
}

// ---- Clipboard ----

const (
	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002
)

// SetClipboard places UTF-16 text on the Windows clipboard (works on Win7).
func SetClipboard(text string) {
	u, err := syscall.UTF16FromString(text)
	if err != nil {
		return
	}
	bytes := len(u) * 2
	hMem, _, _ := pGlobalAlloc.Call(GMEM_MOVEABLE, uintptr(bytes))
	if hMem == 0 {
		return
	}
	ptr, _, _ := pGlobalLock.Call(hMem)
	if ptr == 0 {
		return
	}
	pCopyMemory.Call(ptr, uintptr(unsafe.Pointer(&u[0])), uintptr(bytes))
	_, _, _ = pGlobalUnlock.Call(hMem)
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return
	}
	_, _, _ = pEmptyClipboard.Call()
	_, _, _ = pSetClipboardData.Call(CF_UNICODETEXT, hMem)
	_, _, _ = pCloseClipboard.Call()
}

// ---- helpers ----

// PathExists reports whether a filesystem path exists.
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// RestartSelf relaunches the current executable (used after toggling tray mode).
func RestartSelf() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe)
	cmd.Start()
}
