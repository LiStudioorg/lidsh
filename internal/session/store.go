// JSONL + zstd 持久化，复刻 dsh-session-persistence-jsonl + dsh-session-format。
//
// 磁盘形态（实测验证，详见 docs/_parts/core-session.md §2）：
//
//	{root}/{projectKey(cwd)}/{encodeSegment(sessionId)}/
//	  session.v3.jsonl.zstd   —— 一个格式世代一个不可变文件
//	  session.lock            —— flock(2) 跨进程写锁，故意无过期，释放后不删除
//
// 文件 = concatenated Zstandard frames：header 与每个持久化批次各压一个独立
// 带校验和的帧，支持只追加不解全档；撕裂尾帧可定位起点并忽略。
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ---------- 路径编码 ----------

// ProjectKey 复刻 projectKey(cwd)（persistence index.js:874-893）：
// 分隔符 / \ : 折叠成单个 '-'；安全字符 [A-Za-z0-9._-] 原样；其余码点转 ~XXXX
// （4 位大写 hex）；去首部 '-'；空→"root"；截断 251；外套 --…--。
// cwd 为空 → "_no-cwd"（index.js:901-904）。
func ProjectKey(cwd string) string {
	if cwd == "" {
		return "_no-cwd"
	}
	var b strings.Builder
	prevDash := false
	for _, r := range cwd {
		switch {
		case r == '/' || r == '\\' || r == ':':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
			prevDash = r == '-'
		default:
			fmt.Fprintf(&b, "~%04X", r)
			prevDash = false
		}
	}
	s := strings.TrimPrefix(b.String(), "-")
	if s == "" {
		s = "root"
	}
	if len(s) > 251 {
		s = s[:251]
	}
	return "--" + s + "--"
}

// EncodeSegment 复刻 encodeSegment(id)（index.js:852-863）：SessionId 是未校验
// 字符串，必须单射编码防路径穿越。'.'→~002E（所以 ".."→~002E~002E 不会撞上
// 父目录），非 [A-Za-z0-9._-] 码点 → ~XXXX。
func EncodeSegment(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			b.WriteRune(r)
		case r == '.':
			b.WriteString("~002E")
		default:
			fmt.Fprintf(&b, "~%04X", r)
		}
	}
	return b.String()
}

// SessionDir 计算会话目录。
func SessionDir(root, cwd, id string) string {
	return filepath.Join(root, ProjectKey(cwd), EncodeSegment(id))
}

// LogFileName 复刻文件名规则（format index.js:472-475 + persistence :746-762）：
// v0 = session.jsonl；v≥1 = session.v{N}.jsonl；压缩再加 .zstd 后缀。
func LogFileName(version int, compression string) string {
	name := "session.jsonl"
	if version >= 1 {
		name = fmt.Sprintf("session.v%d.jsonl", version)
	}
	if compression != "none" {
		name += ".zstd"
	}
	return name
}

const leaseFilename = "session.lock"

// ---------- zstd 帧容器 ----------

// zstdEncoder/Decoder 复用安全（EncodeAll/DecodeAll 并发安全）。
var (
	zstdEnc *zstd.Encoder
	zstdDec *zstd.Decoder
)

func init() {
	var err error
	zstdEnc, err = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic("session: zstd encoder: " + err.Error())
	}
	zstdDec, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		panic("session: zstd decoder: " + err.Error())
	}
}

// CompressFrame 把一个批次压成单个独立帧（compressZstdFrame）。
func CompressFrame(plain []byte) []byte {
	return zstdEnc.EncodeAll(plain, nil)
}

// ScanZstdFrames 不解压定位所有**完整**帧的边界（scanZstdFrames）：返回完整帧
// 拼接后的总字节数。尾部撕裂帧被排除在外，调用方可截到该偏移后继续追加。
// 输入若非 zstd（compression:'none'）由调用方走明文字节路径。
func ScanZstdFrames(data []byte) (int, error) {
	// 帧魔数 0xFD2FB528（小端 28 B5 2F FD）。
	const magic = 0xFD2FB528
	off := 0
	for off < len(data) {
		if len(data)-off < 4 {
			return off, nil // 撕裂的魔数头
		}
		if le32(data[off:]) != magic {
			return off, fmt.Errorf("zstd: unexpected bytes at offset %d (not a frame)", off)
		}
		frameLen, err := zstdFrameLen(data[off:])
		if err != nil {
			return off, nil // 撕裂帧头：到不了下一个完整帧
		}
		if frameLen == nil {
			// 帧头声明 ContentSize 未知：内容到显式 EndMark。扫描压缩块不可靠，
			// 用解压边界：交给 decodeAllPrefix 的降级路径处理（见 ReadLog）。
			return off, errUnknownContentSize
		}
		if off+*frameLen > len(data) {
			return off, nil // 撕裂帧体
		}
		off += *frameLen
	}
	return off, nil
}

var errUnknownContentSize = errors.New("zstd: frame with unknown content size")

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// zstdFrameLen 解析帧头算出整帧字节数（含 magic）。RFC 8878 §3.1.1。
func zstdFrameLen(b []byte) (*int, error) {
	if len(b) < 5 {
		return nil, io.ErrUnexpectedEOF
	}
	fhd := b[4]
	frameContentSizeFlag := (fhd >> 6) & 3
	singleSegments := (fhd>>5)&1 == 1
	contentChecksum := (fhd>>2)&1 == 1
	filled := (fhd>>1)&1 == 1 // unused
	dictionaryIDFlag := fhd & 3
	if filled {
		return nil, errors.New("zstd: reserved bit set")
	}
	pos := 5
	if !singleSegments {
		if pos >= len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		pos++ // Window Descriptor
	}
	switch dictionaryIDFlag {
	case 1:
		pos += 1
	case 2:
		pos += 2
	case 3:
		pos += 4
	}
	var contentSize int
	switch frameContentSizeFlag {
	case 0:
		if singleSegments {
			if pos >= len(b) {
				return nil, io.ErrUnexpectedEOF
			}
			contentSize = int(b[pos])
			pos++
		}
		// contentSize 未知（flag=0 且非单段）：0 仅作 scanBlockStream 参考。
	case 1:
		if pos+2 > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		contentSize = int(uint16(b[pos]) | uint16(b[pos+1])<<8)
		pos += 2
	case 2:
		if pos+4 > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		contentSize = int(le32(b[pos:]))
		pos += 4
	case 3:
		if pos+8 > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		var v uint64
		for i := 0; i < 8; i++ {
			v |= uint64(b[pos+i]) << (8 * i)
		}
		contentSize = int(v)
		pos += 8
	}
	// 块头的 size 字段是存储字节数、自描述；定位帧尾不需要 contentSize。
	blocksEnd, err := scanBlockStream(b[pos:], contentSize)
	if err != nil {
		return nil, err
	}
	total := pos + blocksEnd
	if contentChecksum {
		total += 4
	}
	return &total, nil
}

// scanBlockStream 扫描压缩块头直到 Last_Block=1（RFC 8878 §3.1.1.2）。
// expectedDecompressed 只用于校验，不影响定位。
func scanBlockStream(b []byte, expectedDecompressed int) (int, error) {
	pos := 0
	for {
		if pos+3 > len(b) {
			return 0, io.ErrUnexpectedEOF
		}
		h := uint32(b[pos]) | uint32(b[pos+1])<<8 | uint32(b[pos+2])<<16
		last := h&1 == 1
		blockType := (h >> 1) & 3
		size := h >> 3
		pos += 3
		// RFC 8878 §3.1.1.2.1：Block_Type 00=Raw, 01=RLE, 10=Compressed,
		// 11=Reserved。块头的 size 字段 = 块的存储字节数（block zsize）。
		switch blockType {
		case 0: // Raw：直接存储未压缩字节，长度 = size
			if pos+int(size) > len(b) {
				return 0, io.ErrUnexpectedEOF
			}
			pos += int(size)
		case 1: // RLE：1 个字节重复解压至 expectedDecompressed
			if pos+1 > len(b) {
				return 0, io.ErrUnexpectedEOF
			}
			pos += 1
		case 2: // Compressed：size = 压缩字节数
			if pos+int(size) > len(b) {
				return 0, io.ErrUnexpectedEOF
			}
			pos += int(size)
		default:
			return 0, errors.New("zstd: reserved block type")
		}
		if last {
			return pos, nil
		}
	}
}

// DecodeAllPrefix 解码 concatenated frames，容忍尾部撕裂帧：返回全部**完整帧**
// 的明文与已消费的输入字节数（decompressZstdPrefix 的等价：从未完成尾帧抢救明文）。
func DecodeAllPrefix(data []byte) (plain []byte, consumed int, err error) {
	out := make([]byte, 0, len(data)*2)
	off := 0
	for off < len(data) {
		if len(data)-off < 4 || le32(data[off:]) != 0xFD2FB528 {
			return out, off, nil
		}
		frameLen, ferr := zstdFrameLen(data[off:])
		if ferr != nil || frameLen == nil {
			// 撕裂帧或未知长度：未知长度时尝试直接解压剩余（decodeAllPrefix
			// 降级路径——尾帧不完整则解压失败，按已消费处理）。
			rest := data[off:]
			dec, derr := zstdDec.DecodeAll(rest, nil)
			if derr != nil {
				return out, off, nil
			}
			out = append(out, dec...)
			return out, len(data), nil
		}
		if off+*frameLen > len(data) {
			return out, off, nil // 撕裂帧体
		}
		dec, derr := zstdDec.DecodeAll(data[off:off+*frameLen], nil)
		if derr != nil {
			return out, off, nil // 校验和失败按撕裂处理
		}
		out = append(out, dec...)
		off += *frameLen
	}
	return out, off, nil
}

// ---------- 日志读写 ----------

// LoadLog 读取一个会话文件，返回 header、事件与可安全追加的字节偏移。
// fail-closed 规则：
//   - header.version 非本 build 支持 → 明确的不支持版本错误（format :960-967）
//   - 未识别事件类型且无 ignorable:true → 拒绝解释日志
//   - 撕裂尾（最后一行缺换行 / 撕裂 zstd 帧）→ 截到安全偏移，仍视为可写
func LoadLog(path string, compression string) (*Header, []*Event, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, err
	}
	var plain []byte
	var safeBytes int64
	if compression == "none" {
		plain, safeBytes, err = splitTornPlain(data)
		if err != nil {
			return nil, nil, 0, err
		}
	} else {
		dec, consumed, derr := DecodeAllPrefix(data)
		if derr != nil {
			return nil, nil, 0, derr
		}
		plain = dec
		safeBytes = int64(consumed)
		// 明文内部再按行撕裂处理。
		plain, _, err = splitTornPlain(plain)
		if err != nil {
			return nil, nil, 0, err
		}
	}

	lines := splitLines(plain)
	if len(lines) == 0 {
		return nil, nil, safeBytes, fmt.Errorf("empty session log %s", path)
	}

	var header Header
	if err := json.Unmarshal(lines[0], &header); err != nil {
		return nil, nil, 0, fmt.Errorf("session header: %w", err)
	}
	if header.Type != "session" {
		return nil, nil, 0, fmt.Errorf("session header: first line must be type=session")
	}
	// 退役字段拒绝（persistence :796-799）。
	var rawHeader map[string]json.RawMessage
	_ = json.Unmarshal(lines[0], &rawHeader)
	for _, retired := range []string{"sandboxMode", "approvalPolicy"} {
		if _, bad := rawHeader[retired]; bad {
			return nil, nil, 0, fmt.Errorf("session header: retired field %q is not allowed", retired)
		}
	}
	if header.Version != FormatVersion {
		return nil, nil, 0, fmt.Errorf("unsupported session format version %d (this build writes %d)", header.Version, FormatVersion)
	}

	events := make([]*Event, 0, len(lines)-1)
	for i, line := range lines[1:] {
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, nil, 0, fmt.Errorf("event line %d: %w", i+2, err)
		}
		if !KnownEventTypes[e.Type] {
			if e.Ignorable == nil || !*e.Ignorable {
				return nil, nil, 0, fmt.Errorf("event line %d: unknown event type %q is not ignorable", i+2, e.Type)
			}
			continue // 带 ignorable 的未知类型安全跳过
		}
		events = append(events, &e)
	}
	return &header, events, safeBytes, nil
}

// splitTornPlain：缺换行的最后一条记录按撕裂尾忽略（format.d.ts:182-186），
// 返回截断后的明文与安全追加偏移。
func splitTornPlain(data []byte) ([]byte, int64, error) {
	if len(data) == 0 {
		return data, 0, nil
	}
	if data[len(data)-1] == '\n' {
		return data, int64(len(data)), nil
	}
	idx := bytes.LastIndexByte(data, '\n')
	if idx < 0 {
		return nil, 0, nil // 整个文件是半行
	}
	return data[:idx+1], int64(idx + 1), nil
}

func splitLines(plain []byte) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(plain, []byte("\n")) {
		l = bytes.TrimSpace(l)
		if len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// LogWriter 增量写一个会话日志：header 一帧 + 每批事件一帧。
type LogWriter struct {
	f           *os.File
	compression string
	written     int64
}

// OpenLogWriter 打开（必要时截掉撕裂尾后）日志文件准备追加。
func OpenLogWriter(path string, compression string, safeBytes int64) (*LogWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() > safeBytes {
		if err := f.Truncate(safeBytes); err != nil {
			f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &LogWriter{f: f, compression: compression, written: safeBytes}, nil
}

// Append 写一批记录（每条一行 JSON + 换行），一个批次一个 zstd 帧。
func (w *LogWriter) Append(lines [][]byte) error {
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	var out []byte
	if w.compression == "none" {
		out = buf.Bytes()
	} else {
		out = CompressFrame(buf.Bytes())
	}
	n, err := w.f.Write(out)
	w.written += int64(n)
	return err
}

// AppendEvent 序列化并追加一个事件。
func (w *LogWriter) AppendEvent(e *Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return w.Append([][]byte{b})
}

func (w *LogWriter) Close() error { return w.f.Close() }

// ---------- 写锁 ----------

// Lease 是 session.lock 的跨进程写锁（flock(2) 语义：持锁 = 打开 handle 期间；
// 进程死亡内核自动释放；故意无过期；释放后不删除文件）。
type Lease struct {
	f *os.File
}

// AcquireLease 非阻塞抢占会话目录的写锁。
func AcquireLease(dir string) (*Lease, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, leaseFilename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flockExclusive(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("session %s is locked by another process", filepath.Base(dir))
	}
	return &Lease{f: f}, nil
}

// Release 释放锁（不删除锁文件——稳定 inode 供后来者校验，lease.d.ts:1-25）。
func (l *Lease) Release() error {
	if l.f == nil {
		return nil
	}
	err := funlock(l.f)
	l.f.Close()
	l.f = nil
	return err
}
