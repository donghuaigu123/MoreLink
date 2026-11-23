//go:build windows

// Package winui is a minimal pure-Go Win32 GUI toolkit (no cgo, no external
// dependencies). It provides windows, buttons, edit boxes, labels and list
// boxes with a modern high-contrast dark theme, plus a simple layout engine.
package winui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	logMu   sync.Mutex
	logPath string
	logOnce sync.Once
)

// InitLog enables writing diagnostics to a file next to the executable.
// Call once at startup. Failures are ignored (GUI app has no console).
func InitLog(name string) {
	logOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "MoreLink")
		_ = os.MkdirAll(dir, 0755)
		logPath = filepath.Join(dir, name)
		Logf("==== %s 启动 %s ====", name, time.Now().Format("2006-01-02 15:04:05"))
	})
}

// Logf appends a diagnostic line to the log file.
func Logf(format string, args ...interface{}) {
	if logPath == "" {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// RecoverPanic should be deferred at the very top of main. Instead of a silent
// crash (invisible in a GUI-subsystem exe), it logs the panic and shows a box.
func RecoverPanic() {
	if r := recover(); r != nil {
		msg := fmt.Sprintf("程序遇到错误，已记录到日志。\n\n%v", r)
		Logf("PANIC: %v", r)
		MessageBox(0, msg, "MoreLink 错误", MB_ICONERROR)
		time.Sleep(1500 * time.Millisecond)
	}
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	pRegisterClassEx    = user32.NewProc("RegisterClassExW")
	pCreateWindowEx     = user32.NewProc("CreateWindowExW")
	pShowWindow         = user32.NewProc("ShowWindow")
	pUpdateWindow       = user32.NewProc("UpdateWindow")
	pGetMessage         = user32.NewProc("GetMessageW")
	pTranslateMessage   = user32.NewProc("TranslateMessage")
	pDispatchMessage    = user32.NewProc("DispatchMessageW")
	pDefWindowProc      = user32.NewProc("DefWindowProcW")
	pPostQuitMessage    = user32.NewProc("PostQuitMessage")
	pSetWindowText      = user32.NewProc("SetWindowTextW")
	pGetWindowText      = user32.NewProc("GetWindowTextW")
	pGetWindowTextLen   = user32.NewProc("GetWindowTextLengthW")
	pMoveWindow         = user32.NewProc("MoveWindow")
	pShowWindowCtrl     = user32.NewProc("ShowWindow")
	pEnableWindow       = user32.NewProc("EnableWindow")
	pInvalidateRect     = user32.NewProc("InvalidateRect")
	pGetClientRect      = user32.NewProc("GetClientRect")
	pBeginPaint         = user32.NewProc("BeginPaint")
	pEndPaint           = user32.NewProc("EndPaint")
	pFillRect           = user32.NewProc("FillRect")
	pDrawText           = user32.NewProc("DrawTextW")
	// FIX: these three are GDI functions, NOT user32. Calling them through
	// user32 caused an immediate panic in WM_PAINT, which silently killed the
	// GUI process (no console) — the true root cause of "won't open".
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkColor         = gdi32.NewProc("SetBkColor")
	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pGetStockObject     = gdi32.NewProc("GetStockObject")
	pCreateFont         = gdi32.NewProc("CreateFontW")
	pGetSysColorBrush   = user32.NewProc("GetSysColorBrush")
	pSendMessage        = user32.NewProc("SendMessageW")
	pSetTimer           = user32.NewProc("SetTimer")
	pKillTimer          = user32.NewProc("KillTimer")
	pGetDlgItem         = user32.NewProc("GetDlgItem")
	pSetFocus           = user32.NewProc("SetFocus")
	pGetOpenFileName    = comdlg32.NewProc("GetOpenFileNameW")
	pMessageBox         = user32.NewProc("MessageBoxW")
	pShellExecute       = user32.NewProc("ShellExecuteW")
	pGetModuleHandle    = kernel32Proc("GetModuleHandleW")
)

func kernel32Proc(name string) *syscall.LazyProc {
	return syscall.NewLazyDLL("kernel32.dll").NewProc(name)
}

type HWND = syscall.Handle
type HBRUSH = syscall.Handle
type HDC = syscall.Handle
type HFONT = syscall.Handle

const (
	WM_CREATE        = 0x0001
	WM_DESTROY       = 0x0002
	WM_SIZE          = 0x0005
	WM_COMMAND       = 0x0111
	WM_PAINT         = 0x000F
	WM_TIMER         = 0x0113
	WM_CLOSE         = 0x0010
	WM_CTLCOLORSTATIC  = 0x0138
	WM_CTLCOLOREDIT    = 0x0133
	WM_CTLCOLORBTN     = 0x0135
	WM_CTLCOLORLISTBOX = 0x0134
	WM_DRAWITEM        = 0x002B
	WM_NOTIFY          = 0x004E
	WM_LBUTTONDOWN     = 0x0201

	BN_CLICKED = 0

	EN_CHANGE = 0x0300

	CBN_SELCHANGE = 1

	LB_ADDSTRING    = 0x0180
	LB_INSERTSTRING = 0x0181
	LB_DELETESTRING = 0x0182
	LB_RESETCONTENT = 0x0184
	LB_GETCOUNT     = 0x018B
	LB_GETCURSEL    = 0x0188
	LB_SETCURSEL    = 0x0186
	LB_GETTEXT      = 0x0189
	LB_GETTEXTLEN   = 0x018A
	LB_SETTOPINDEX  = 0x0197

	EM_SETREADONLY = 0x00CF

	SW_SHOW     = 5
	SW_HIDE     = 0
	SW_MAXIMIZE = 3

	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_BORDER           = 0x00800000
	WS_VSCROLL          = 0x00200000
	WS_TABSTOP          = 0x00010000
	WS_CLIPCHILDREN     = 0x02000000

	BS_PUSHBUTTON = 0
	BS_OWNERDRAW  = 0x0000000B

	ES_LEFT       = 0
	ES_AUTOHSCROLL = 0x0080
	ES_MULTILINE  = 0x0004
	ES_READONLY   = 0x0800

	SS_LEFT = 0

	LBS_STANDARD = 0x00A00003
	LBS_NOTIFY   = 0x0001

	ODS_SELECTED = 0x0001

	DT_CENTER     = 0x0001
	DT_VCENTER    = 0x0004
	DT_SINGLELINE = 0x0020
	DT_LEFT       = 0
	DT_WORDBREAK  = 0x0010
	DT_CALCRECT   = 0x0400

	FW_BOLD = 700

	TRANSPARENT = 1
	OPAQUE      = 2

	COLOR_WINDOW    = 5
	COLOR_BTNTEXT   = 18
	COLOR_WINDOWTEXT = 8

	MB_OK            = 0
	MB_ICONINFORMATION = 0x40
	MB_ICONERROR       = 0x10
	MB_ICONQUESTION    = 0x20
	MB_YESNO         = 0x04

	IDOK    = 1
	IDCANCEL = 2
)

type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground HBRUSH
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       syscall.Handle
}

type MSG struct {
	Hwnd    HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type RECT struct {
	Left, Top, Right, Bottom int32
}

type PAINTSTRUCT struct {
	Hdc         HDC
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type DRAWITEMSTRUCT struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   HWND
	HDC        HDC
	RcItem     RECT
	ItemData   uintptr
}

type OPENFILENAME struct {
	StructSize      uint32
	Owner           HWND
	Instance        syscall.Handle
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustFilter   uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefExt          *uint16
	CustData        uintptr
	FnHook          uintptr
	TemplateName    *uint16
	PvReserved      uintptr
	DwReserved      uint32
	FlagsEx         uint32
}

const OFN_FILEMUSTEXIST = 0x00001000
const OFN_PATHMUSTEXIST = 0x00000800

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func rgb(r, g, b byte) uintptr {
	return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16
}

// Palette — high contrast dark theme.
var (
	ClrBg       = rgb(11, 15, 25)
	ClrPanel    = rgb(20, 27, 44)
	ClrPanel2   = rgb(27, 36, 58)
	ClrText     = rgb(230, 237, 247)
	ClrMuted    = rgb(139, 152, 173)
	ClrAccent   = rgb(110, 231, 183)
	ClrAccent2  = rgb(56, 189, 248)
	ClrDanger   = rgb(248, 113, 113)
	ClrHeader   = rgb(16, 52, 58)
)

var (
	brushBg      HBRUSH
	brushPanel   HBRUSH
	brushAccent  HBRUSH
	brushAccent2 HBRUSH
	brushDanger  HBRUSH
	fontUI       HFONT
	fontTitle    HFONT
)

// Control is a handle to a native Win32 child window.
type Control struct {
	H HWND
	ID uintptr
}

// Window represents the main application window.
type Window struct {
	H             HWND
	Title         string
	Width, Height int
	OnCommand     func(id uintptr)
	OnCreate      func()
	OnTimer       func()
	OnDrawItem    func(*DRAWITEMSTRUCT)
	OnPaintExtra  func(hdc HDC, rc RECT)
	Controls      map[uintptr]*Control
	nextID        uintptr
	wndProc       uintptr
}

func NewWindow(title string, width, height int) *Window {
	return &Window{
		Title: title, Width: width, Height: height,
		Controls: map[uintptr]*Control{}, nextID: 1000,
	}
}

func (w *Window) nextCtrlID() uintptr {
	id := w.nextID
	w.nextID++
	return id
}

func (w *Window) addControl(h HWND) *Control {
	c := &Control{H: h, ID: w.nextCtrlID()}
	w.Controls[c.ID] = c
	return c
}

// Create builds and shows the window. It blocks in the message loop.
func (w *Window) Create() error {
	brushBg = HBRUSH(createBrush(ClrBg))
	brushPanel = HBRUSH(createBrush(ClrPanel))
	brushAccent = HBRUSH(createBrush(ClrAccent))
	brushAccent2 = HBRUSH(createBrush(ClrAccent2))
	brushDanger = HBRUSH(createBrush(ClrDanger))
	fontUI = createFont(17, false)
	fontTitle = createFont(26, true)

	hinst, _, _ := pGetModuleHandle.Call(0)
	className := utf16("MoreLinkWinClass")

	w.wndProc = syscall.NewCallback(func(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
		return w.proc(hwnd, msg, wp, lp)
	})

	wc := &WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		Style:         0x0002 | 0x0008, // CS_HREDRAW | CS_VREDRAW
		LpfnWndProc:   w.wndProc,
		HInstance:     syscall.Handle(hinst),
		HbrBackground: brushBg,
		LpszClassName: className,
	}
	_, _, err := pRegisterClassEx.Call(uintptr(unsafe.Pointer(wc)))
	if err != nil && err != syscall.Errno(0) {
		// Class may already be registered; ignore.
	}

	h, _, err := pCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16(w.Title))),
		WS_OVERLAPPEDWINDOW|WS_VISIBLE|WS_CLIPCHILDREN,
		80, 60, uintptr(w.Width), uintptr(w.Height),
		0, 0, uintptr(hinst), 0,
	)
	if h == 0 {
		return err
	}
	w.H = HWND(h)

	pShowWindow.Call(uintptr(h), SW_SHOW)
	pUpdateWindow.Call(uintptr(h))

	var m MSG
	for {
		ret, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func (w *Window) proc(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_CREATE:
		// CRITICAL FIX: WM_CREATE is dispatched synchronously from inside
		// CreateWindowEx, BEFORE Create() assigns w.H. All child controls
		// created in OnCreate use w.H as parent; without this they get parent=0
		// and fail to appear (blank window). Use the hwnd passed to the proc.
		w.H = hwnd
		pSetTimer.Call(uintptr(hwnd), 1, 400, 0)
		if w.OnCreate != nil {
			w.OnCreate()
		}
		return 0

	case WM_DESTROY:
		pKillTimer.Call(uintptr(hwnd), 1)
		pPostQuitMessage.Call(0)
		return 0

	case WM_CLOSE:
		pPostQuitMessage.Call(0)
		return 0

	case WM_TIMER:
		if w.OnTimer != nil {
			w.OnTimer()
		}
		return 0

	case WM_COMMAND:
		id := wp & 0xFFFF
		if w.OnCommand != nil {
			w.OnCommand(id)
		}
		return 0

	case WM_DRAWITEM:
		if w.OnDrawItem != nil {
			dis := (*DRAWITEMSTRUCT)(unsafe.Pointer(lp))
			w.OnDrawItem(dis)
			return 0
		}

	case WM_PAINT:
		var ps PAINTSTRUCT
		hdc, _, _ := pBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
		var rc RECT
		pGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
		// Header band.
		rcH := RECT{Left: 0, Top: 0, Right: rc.Right, Bottom: 74}
		brushHeader := HBRUSH(createBrush(ClrHeader))
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rcH)), uintptr(brushHeader))
		pDeleteObject.Call(uintptr(brushHeader))
		pSetBkMode.Call(hdc, TRANSPARENT)
		pSetTextColor.Call(hdc, ClrAccent)
		pSelectObject.Call(hdc, uintptr(fontTitle))
		txt := "◆ MoreLink  ·  跨设备协同中心"
		pDrawText.Call(hdc, uintptr(unsafe.Pointer(utf16(txt))), uintptr(len([]rune(txt))),
			uintptr(unsafe.Pointer(&rcH)), DT_LEFT|DT_VCENTER|DT_SINGLELINE)
		pSelectObject.Call(hdc, uintptr(fontUI))
		if w.OnPaintExtra != nil {
			w.OnPaintExtra(HDC(hdc), rc)
		}
		pEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
		return 0

	case WM_CTLCOLORSTATIC, WM_CTLCOLOREDIT, WM_CTLCOLORLISTBOX, WM_CTLCOLORBTN:
		hdc := HDC(wp)
		pSetTextColor.Call(uintptr(hdc), ClrText)
		pSetBkColor.Call(uintptr(hdc), ClrPanel)
		return uintptr(brushPanel)
	}
	ret, _, _ := pDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return ret
}

func createBrush(color uintptr) HBRUSH {
	h, _, _ := pCreateSolidBrush.Call(color)
	return HBRUSH(h)
}

func createFont(height int, bold bool) HFONT {
	weight := uintptr(400)
	if bold {
		weight = FW_BOLD
	}
	h, _, _ := pCreateFont.Call(
		uintptr(height), 0, 0, 0, weight, 0, 0, 0,
		0, 0, 0, 0, 0, uintptr(unsafe.Pointer(utf16("Microsoft YaHei"))),
	)
	if h == 0 {
		h, _, _ = pCreateFont.Call(
			uintptr(height), 0, 0, 0, weight, 0, 0, 0,
			0, 0, 0, 0, 0, uintptr(unsafe.Pointer(utf16("SimSun"))),
		)
	}
	return HFONT(h)
}

func (w *Window) createCtrl(class, text string, style uint32, x, y, width, height int, parent HWND) *Control {
	if parent == 0 {
		parent = w.H
	}
	id := w.nextCtrlID()
	h, _, _ := pCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(WS_CHILD|WS_VISIBLE|style),
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		uintptr(parent), uintptr(id), 0, 0,
	)
	pSendMessage.Call(h, 0x0030 /*WM_SETFONT*/, uintptr(fontUI), 1)
	c := &Control{H: HWND(h), ID: id}
	w.Controls[id] = c
	return c
}

// Label creates a static text control.
func (w *Window) Label(text string, x, y, width, height int) *Control {
	return w.createCtrl("STATIC", text, SS_LEFT, x, y, width, height, 0)
}

// Edit creates a single-line edit control.
func (w *Window) Edit(text string, x, y, width, height int) *Control {
	return w.createCtrl("EDIT", text, WS_BORDER|ES_LEFT|ES_AUTOHSCROLL|WS_TABSTOP, x, y, width, height, 0)
}

// MultiEdit creates a read-only multiline edit (log output).
func (w *Window) MultiEdit(x, y, width, height int) *Control {
	c := w.createCtrl("EDIT", "", WS_BORDER|ES_MULTILINE|ES_READONLY|WS_VSCROLL|0x00000080, x, y, width, height, 0)
	return c
}

// Button creates a standard push button with an auto-generated command ID.
func (w *Window) Button(text string, x, y, width, height int) *Control {
	return w.createCtrl("BUTTON", text, BS_PUSHBUTTON|WS_TABSTOP, x, y, width, height, 0)
}

// ButtonID creates a push button with an explicit command ID, so WM_COMMAND
// can dispatch on a stable constant.
func (w *Window) ButtonID(id uintptr, text string, x, y, width, height int) *Control {
	h, _, _ := pCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(utf16("BUTTON"))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON|WS_TABSTOP),
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		uintptr(w.H), id, 0, 0,
	)
	pSendMessage.Call(h, 0x0030, uintptr(fontUI), 1)
	c := &Control{H: HWND(h), ID: id}
	w.Controls[id] = c
	return c
}

// ListBox creates a list box control.
func (w *Window) ListBox(x, y, width, height int) *Control {
	return w.createCtrl("LISTBOX", "", LBS_STANDARD|WS_VSCROLL, x, y, width, height, 0)
}

func (c *Control) SetText(text string) {
	pSetWindowText.Call(uintptr(c.H), uintptr(unsafe.Pointer(utf16(text))))
}

func (c *Control) Text() string {
	n, _, _ := pGetWindowTextLen.Call(uintptr(c.H))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	pGetWindowText.Call(uintptr(c.H), uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func (c *Control) SetEnabled(on bool) {
	v := 0
	if on {
		v = 1
	}
	pEnableWindow.Call(uintptr(c.H), uintptr(v))
}

func (c *Control) ListAdd(text string) {
	pSendMessage.Call(uintptr(c.H), LB_ADDSTRING, 0, uintptr(unsafe.Pointer(utf16(text))))
}

func (c *Control) ListClear() {
	pSendMessage.Call(uintptr(c.H), LB_RESETCONTENT, 0, 0)
}

func (c *Control) ListCount() int {
	r, _, _ := pSendMessage.Call(uintptr(c.H), LB_GETCOUNT, 0, 0)
	return int(r)
}

func (c *Control) ListCurSel() int {
	r, _, _ := pSendMessage.Call(uintptr(c.H), LB_GETCURSEL, 0, 0)
	return int(r)
}

func (c *Control) ListSetCurSel(i int) {
	pSendMessage.Call(uintptr(c.H), LB_SETCURSEL, uintptr(i), 0)
}

func (c *Control) ListText(i int) string {
	n, _, _ := pSendMessage.Call(uintptr(c.H), LB_GETTEXTLEN, uintptr(i), 0)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	pSendMessage.Call(uintptr(c.H), LB_GETTEXT, uintptr(i), uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

// AppendLog appends a line to a multiline edit control.
func (c *Control) AppendLog(line string) {
	n, _, _ := pGetWindowTextLen.Call(uintptr(c.H))
	pSendMessage.Call(uintptr(c.H), 0x00B1 /*EM_SETSEL*/, n, n)
	text := line + "\r\n"
	pSendMessage.Call(uintptr(c.H), 0x0082 /*EM_REPLACESEL*/, 1, uintptr(unsafe.Pointer(utf16(text))))
}

// OpenFileDialog shows a native file picker and returns the selected path.
func OpenFileDialog(owner HWND, title, filter string) string {
	buf := make([]uint16, 4096)
	buf[0] = 0
	ofn := &OPENFILENAME{
		StructSize:    uint32(unsafe.Sizeof(OPENFILENAME{})),
		Owner:         owner,
		Filter:        utf16(filter),
		File:          &buf[0],
		MaxFile:       4096,
		Title:         utf16(title),
		Flags:         OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST,
	}
	ok, _, _ := pGetOpenFileName.Call(uintptr(unsafe.Pointer(ofn)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// MessageBox shows a native message box.
func MessageBox(owner HWND, text, title string, flags uint32) {
	pMessageBox.Call(uintptr(owner), uintptr(unsafe.Pointer(utf16(text))),
		uintptr(unsafe.Pointer(utf16(title))), uintptr(flags))
}

// ShellOpen opens a file or URL with the default handler.
func ShellOpen(target string) {
	pShellExecute.Call(0, uintptr(unsafe.Pointer(utf16("open"))),
		uintptr(unsafe.Pointer(utf16(target))), 0, 0, SW_SHOW)
}
