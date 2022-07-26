// @author cold bin
// @date 2022/7/23

package server

import (
	"compress/flate"
	"errors"
	"fmt"
	"log"
	"mini-websocket/tool"
	"net/http"
	"time"
)

const (
	minHandshakeTimeout = time.Second //握手最少超时时间
)

//压缩等级
const (
	NoCompression      = flate.NoCompression
	BestSpeed          = flate.BestSpeed
	BestCompression    = flate.BestCompression
	DefaultCompression = flate.DefaultCompression
	HuffmanOnly        = flate.HuffmanOnly
)

var DefaultUpGrader = upGrader{
	HandshakeTimeout: minHandshakeTimeout,
	ReadBufferSize:   minReadBufferSize,
	WriteBufferSize:  minWriteBufferSize,
	OnError:          defaultOnErr,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	CompressLevel: 0,
}

// UpGrader 指定将http连接劫持升级为websocket连接
type upGrader struct {
	//握手超时时间
	HandshakeTimeout time.Duration
	//指定底层网络连接的缓冲区大小
	ReadBufferSize, WriteBufferSize int
	//错误的处理函数
	OnError func(w http.ResponseWriter, status int, reason string)
	//跨域支持
	CheckOrigin func(r *http.Request) bool
	//压缩等级
	CompressLevel int
}

// NewUpGrader 如果为提供 OnError 参数，将默认使用默认错误处理逻辑
func NewUpGrader(handshakeTimeout time.Duration, RBufSize, WBufSize int, OnErr func(http.ResponseWriter, int, string), checkOrigin func(*http.Request) bool, compressLevel int) upGrader {
	if OnErr == nil {
		OnErr = defaultOnErr
	}
	if checkOrigin == nil {
		checkOrigin = defaultCheckOrigin
	}
	if handshakeTimeout < minHandshakeTimeout {
		handshakeTimeout = minHandshakeTimeout
	}
	if RBufSize < minReadBufferSize || RBufSize > maxReadBufferSize {
		RBufSize = minReadBufferSize
	}
	if WBufSize < minWriteBufferSize || WBufSize > maxWriteBufferSize {
		WBufSize = minWriteBufferSize
	}

	if compressLevel < HuffmanOnly || compressLevel > BestCompression {
		compressLevel = NoCompression //默认无压缩
	}
	return upGrader{
		HandshakeTimeout: handshakeTimeout,
		ReadBufferSize:   RBufSize,
		WriteBufferSize:  WBufSize,
		OnError:          OnErr,
		CheckOrigin:      checkOrigin,
		CompressLevel:    compressLevel,
	}
}

func defaultOnErr(w http.ResponseWriter, status int, reason string) {
	//设置返回值类型
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)

	if _, err := fmt.Fprintln(w, http.StatusText(status), "\taside reason: ", reason); err != nil {
		log.Println("defaultOnError wrong: ", err)
		return
	}
}

// defaultCheckOrigin 默认检查跨域
func defaultCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if len(origin) == 0 {
		return true
	}

	url, err := r.URL.Parse(origin)
	if err != nil {
		return false
	}

	return url.Scheme == r.URL.Scheme && url.Host == r.Host
}

// Error 升级遇到错误时调用
func (ug *upGrader) Error(w http.ResponseWriter, status int, reason string) (*WsConn, error) {
	ug.OnError(w, status, reason)
	err := errors.New(reason)
	return nil, err
}

func (ug *upGrader) UpGrade(r *http.Request, w http.ResponseWriter) (conn *WsConn, err error) {
	//开始握手
	start := time.Now()
	//校验http请求的头部字段，确定是否为握手请求
	if !tool.IsWsHeader(r.Header, "Connection", "Upgrade") {
		return ug.Error(w, http.StatusBadRequest, "'Upgrade' 字段没包含在 'Connection' 字段内")
	}

	if !tool.IsWsHeader(r.Header, "Upgrade", "websocket") {
		return ug.Error(w, http.StatusBadRequest, "'websocket' 没有包含在 'Upgrade' 内")
	}

	if r.Method != http.MethodGet {
		return ug.Error(w, http.StatusMethodNotAllowed, "请求方法不是get方法")
	}

	if !tool.IsWsHeader(r.Header, "Sec-Websocket-Version", "13") {
		return ug.Error(w, http.StatusUpgradeRequired, "请求头不包含服务端支持websocket版本")
	}

	//处理跨域
	if !ug.CheckOrigin(r) {
		return ug.Error(w, http.StatusForbidden, "不允许跨域")
	}

	//随机字符串
	SWK := r.Header.Get("Sec-WebSocket-Key")

	if !tool.IsSWK(SWK) {
		return ug.Error(w, http.StatusBadRequest, "请求头应包含Sec-WebSocket-Key字段的24位随机字符串,wrong: "+SWK)
	}

	//若为握手请求，将劫持http服务器持有的连接塞到websocket里
	h, ok := w.(http.Hijacker)
	if !ok {
		return ug.Error(w, http.StatusInternalServerError, "不能劫持http请求")
	}

	netConn, brw, err := h.Hijack()
	if err != nil {
		return ug.Error(w, http.StatusInternalServerError, "不能劫持http请求")
	}

	//拼接响应数据
	_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = brw.WriteString("Connection:upgrade\r\n")
	_, _ = brw.WriteString("Upgrade:websocket\r\n")
	_, _ = brw.WriteString("Sec-WebSocket-Accept:" + tool.EncodeSWK(SWK) + "\r\n\r\n")

	if err = brw.Flush(); err != nil {
		_ = netConn.Close()
		log.Printf("Upgrader.Upgrade could not write response, err=%v", err)
		return nil, err
	}

	//建立连接
	wsConn := NewWsConn(netConn, true, ug.ReadBufferSize, ug.WriteBufferSize, ug.CompressLevel)

	wsConn.State = Connected

	//握手超时处理
	if start.Add(ug.HandshakeTimeout).Before(time.Now()) {
		err = wsConn.CloseAbnormal()
		return nil, err
	}

	return wsConn, nil
}
