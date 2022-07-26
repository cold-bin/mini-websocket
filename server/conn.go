// @author cold bin
// @date 2022/7/23

package server

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mini-websocket/dispatch"

	"net"
)

const (
	SendCriticalSize = 1024 * 1000 * 2 //发送数据大小限制，避免恶意数据
	shardSize        = 65535           //分片大小

	minReadBufferSize  = 65535 //网络连接缓冲区的读的最少字节数
	minWriteBufferSize = 65535 //网络连接缓冲区的读的少字节数

	maxReadBufferSize  = 65535 * 10 //网络连接缓冲区的读的最多字节数
	maxWriteBufferSize = 65535 * 10 //网络连接缓冲区的写的最多字节数
)

//连接状态
const (
	Connecting = iota + 1
	Connected
	Closing
	Closed
)

// WsConn websocket连接
type WsConn struct {
	Conn  net.Conn
	BufRD *bufio.Reader // 读，缓冲区的数据
	BufWR *bufio.Writer // 写，缓冲区的数据

	State         int  //当前连接的状态
	IsServer      bool //标记服务端，服务端向客户端发送帧数据时，不需要掩码处理
	CompressLevel int  //压缩等级
}

// NewWsConn 构造websocket.Conn
func NewWsConn(netConn net.Conn, isServer bool, ReadBufferSize, WriteBufferSize int, compressLevel int) *WsConn {
	if ReadBufferSize < minReadBufferSize || ReadBufferSize > maxReadBufferSize {
		ReadBufferSize = minReadBufferSize
	}

	if WriteBufferSize < minWriteBufferSize || WriteBufferSize > maxWriteBufferSize {
		WriteBufferSize = minWriteBufferSize
	}

	c := &WsConn{
		Conn:          netConn,
		IsServer:      isServer,
		BufRD:         bufio.NewReaderSize(netConn, ReadBufferSize),
		BufWR:         bufio.NewWriterSize(netConn, WriteBufferSize),
		State:         Connecting,
		CompressLevel: compressLevel,
	}

	return c
}

func (wc *WsConn) LocalAddr() net.Addr {
	return wc.Conn.LocalAddr()
}

func (wc *WsConn) RemoteAddr() net.Addr {
	return wc.Conn.RemoteAddr()
}

// ReadMessage 读取text、binary、延续帧
func (wc *WsConn) ReadMessage() (mt dispatch.MessageType, msg []byte, err error) {
	frame, err := readFrame(wc)
	if err != nil {
		log.Printf("Conn.ReadMessage failed to c.readFrame, err=%v\n", err)
		return dispatch.NoFrame, nil, err
	}
	mt = dispatch.MessageType(frame.OpCode)

	frame.MaskPayload()
	log.Println("frame decode mask payload: ", string(frame.Payload))

	// 根据读到的帧判断是否还有后续的帧，如果有分片，那就读完将payload组装到一起
	buf := bytes.NewBuffer(nil)
	buf.Write(frame.Payload)

	for !frame.IsFinal() {
		if frame, err = readFrame(wc); err != nil {
			log.Printf("Conn.ReadMessage failed to c.readFrame, err=%v", err)
			return dispatch.NoFrame, nil, err
		}

		frame.MaskPayload()
		log.Println("frame decode mask payload: ", string(frame.Payload))
		// todo
		//zipBytes := tool.UGZipBytes(frame.Payload)
		//log.Println(len(zipBytes), zipBytes)

		//buf.Write(zipBytes)
		buf.Write(frame.Payload)
	}

	msg = buf.Bytes()
	return
}

// read 从网络缓冲区取出指定字节数量的数据
func read(wc *WsConn, n int) ([]byte, error) {
	//谨慎探测
	p, err := wc.BufRD.Peek(n)
	if err == io.EOF {
		return nil, err
	}
	//探测没问题
	_, _ = wc.BufRD.Discard(len(p))
	return p, err
}

// readFrame 从字节流里读出一个完整帧的数据，由于控制帧只能是一个帧，所以在读取帧的时候应当处理完控制帧
func readFrame(wc *WsConn) (*dispatch.Frame, error) {
	//1000 0001 1000 1011
	// 读2Byte的数据，取出字节流的帧头部
	p, err := read(wc, 2)
	if err != nil {
		log.Printf("Conn.readFrame failed to c.read(header), err=%v", err)
		return nil, err
	}
	log.Println("Conn.readFrame got frameWithoutPayload bytes: ", p)
	// 解析WebSocket帧头部
	frameWithoutPayload := dispatch.ParseToFrameHeader(p)
	log.Printf("Conn.readFrame got frameWithoutPayload=%+v", frameWithoutPayload)

	remainBytesNum := uint64(frameWithoutPayload.PayloadLen) //payload的字节数，默认等于PayloadLen

	// 126 : 16bits，2bytes
	// 127 : 64bits，8bytes
	switch frameWithoutPayload.PayloadLen {
	case 126:
		//再从p中取出2个字节
		if p, err = read(wc, 2); err != nil {
			log.Printf("Conn.readFrame failed to c.read(2) payloadlen with 16bit, err=%v", err)
			return nil, err
		}
		payloadExtLen16 := binary.BigEndian.Uint16(p[:2])
		frameWithoutPayload.PayloadExtendLen16 = payloadExtLen16
		remainBytesNum = uint64(payloadExtLen16)

	case 127:
		// 再从p中取出8个字节
		if p, err = read(wc, 8); err != nil {
			log.Printf("Conn.readFrame failed to c.read(8) payloadlen with 16bit, err=%v", err)
			return nil, err
		}
		payloadExtLen64 := binary.BigEndian.Uint64(p[:8])
		frameWithoutPayload.PayloadExtendLen64 = payloadExtLen64
		remainBytesNum = payloadExtLen64
	}

	if frameWithoutPayload.Mask == 1 {
		// 再从字节流里读出4个字节的maskingKey
		if p, err = read(wc, 4); err != nil {
			log.Printf("Conn.readFrame failed to c.read(header), err=%v", err)
			return nil, err
		}
		frameWithoutPayload.MaskingKey = binary.BigEndian.Uint32(p)
	}

	// frame校验，先校验非负载数据
	if err = dispatch.CheckFrameWithoutPayload(frameWithoutPayload); err != nil {
		log.Printf("Conn.readFrame failed to c.read(header), err=%v", err)
		if err := wc.CloseWrongProtocol(); err != nil {
			return nil, err
		}
		return nil, err
	}

	// 读取payload数据并填充到frame中去
	payload := make([]byte, 0, remainBytesNum)
	log.Printf("Conn.readFrame c.read(%d) into payload data", remainBytesNum)

	// WsConn.Read 能接受的类型是 int 不能读取太大的数据，避免大类型强转小类型数据丢失
	// -> uint64 > int(64位os里是有符号)；因此需要分片读取
	for remainBytesNum > shardSize {
		//数据可能变小了: wc.Read(int(remainBytesNum))
		p, err := read(wc, shardSize)
		if err != nil {
			log.Printf("Conn.readFrame failed to c.read(payload), err=%v", err)
			return nil, err
		}
		payload = append(payload, p...)
		remainBytesNum -= shardSize
	}

	// 读取剩余部分的payload
	p, err = read(wc, int(remainBytesNum))
	if err != nil {
		log.Printf("Conn.readFrame failed to c.read(payload), err=%v", err)
		return nil, err
	}

	payload = append(payload, p...)

	frameWithoutPayload.Payload = payload

	// 只处理ping pong close 帧
	switch dispatch.MessageType(frameWithoutPayload.OpCode) {
	case dispatch.TextFrame, dispatch.BinaryFrame, dispatch.ContinuationFrame:
		//不处理可能有连续帧的类型
	case dispatch.PingFrame:
		err = wc.ReplyPing(frameWithoutPayload)
	case dispatch.PongFrame:
		err = wc.ReplyPong()
	case dispatch.ConnectionCloseFrame:
		//解析帧中收到关闭帧时，正常关闭
		err = wc.CloseRight()
	default:
		//不能接受的类型 TODO: 扩展协议接受类型
		err = wc.CloseNotAccept()
	}

	return frameWithoutPayload, err
}

func (wc *WsConn) Ping() (err error) {
	return sendControlFrame(wc, dispatch.PingFrame, []byte("ping"))
}

// ReplyPing 响应ping帧数据，将ping帧的负载数据，装入pong帧中即可
func (wc *WsConn) ReplyPing(frame *dispatch.Frame) (err error) {
	return wc.Pong(frame.Payload)
}

func (wc *WsConn) Pong(pingPayload []byte) (err error) {
	return sendControlFrame(wc, dispatch.PongFrame, pingPayload)
}

// ReplyPong 响应pong帧
func (wc *WsConn) ReplyPong() (err error) {
	return wc.Ping()
}

// SendMessage 发送text数据
func (wc *WsConn) SendMessage(text string) (err error) {
	//zipBytes, err := tool.GZipBytes([]byte(text), wc.CompressLevel)
	//if err != nil {
	//	if err := wc.CloseInternalError(); err != nil {
	//		return err
	//	}
	//	return err
	//}
	//fmt.Println(len(zipBytes), zipBytes)
	// todo
	return sendDataFrame(wc, []byte(text), dispatch.TextFrame)
}

// SendBinary 发送二进制数据
func (wc *WsConn) SendBinary(r io.Reader) (err error) {
	payload, err := ioutil.ReadAll(r)
	if err != nil {
		log.Printf("c.SendBinary failed to ioutil.ReadAll, err=%v", err)
		return err
	}

	return sendDataFrame(wc, payload, dispatch.BinaryFrame)
}

// sendDataFrame 发送数据帧：opcode应限制为text和binary
func sendDataFrame(wc *WsConn, data []byte, opcode dispatch.MessageType) (err error) {
	switch opcode {
	case dispatch.TextFrame, dispatch.BinaryFrame:
	default:
		return fmt.Errorf("invalid opcode=%d for data frame", opcode)
	}

	log.Printf("data frame...")

	if len(data) > SendCriticalSize {
		return wc.CloseTooBigData()
	}

	//分片传输
	if len(data) > shardSize {
		frames := fragmentDataFrames(data, wc.IsServer, opcode)
		for _, frame := range frames {
			if err = sendFrame(wc, frame); err != nil {
				log.Printf("c.send failed to c.sendFrame err=%v", err)
				return
			}
		}
		return
	}

	//未分片传输
	frame := constructDataFrame(data, wc.IsServer, opcode)
	if err = sendFrame(wc, frame); err != nil {
		log.Printf("c.send failed to c.sendFrame err=%v", err)
		return
	}

	return
}

// fragmentDataFrames 将大片数据拆分成若干 shardSize 大小的数据
func fragmentDataFrames(data []byte, noMask bool, opcode dispatch.MessageType) []*dispatch.Frame {
	s := len(data)
	start, end, n := 0, 0, s/shardSize

	frames := make([]*dispatch.Frame, 0, n+1)

	//将大数据分成 s / shardSize 份，每份 shardSize
	for i := 1; i <= n; i++ {
		start, end = (i-1)*shardSize, i*shardSize
		frames = append(frames, constructDataFrame(data[start:end], noMask, dispatch.ContinuationFrame))
	}

	//追加剩余数据
	if end < s {
		frames = append(frames, constructDataFrame(data[end:], noMask, dispatch.ContinuationFrame))
	}

	//分片传输，第一个帧的opcode设置为0x1或0x2，其余位都是0
	frames[0].OpCode = uint16(opcode)

	//最后一位的Fin为1,其余要设置为0
	frames[len(frames)-1].Fin = 1

	return frames
}

// sendControlFrame 发送控制帧（ping、pong、close）
func sendControlFrame(wc *WsConn, msgType dispatch.MessageType, payload []byte) (err error) {
	frame := constructControlFrame(msgType, wc.IsServer, payload)
	log.Printf("control frame...")
	if err = sendFrame(wc, frame); err != nil {
		log.Printf("c.send failed to c.sendFrame err=%v", err)
		return
	}

	return nil
}

// sendFrame 发送完好的帧到连接里
func sendFrame(wc *WsConn, frame *dispatch.Frame) error {
	//校验帧
	if err := dispatch.CheckFrameWithoutPayload(frame); err != nil {
		//协议问题关闭连接
		if err := wc.CloseWrongProtocol(); err != nil {
			return err
		}
		return err
	}

	//序列化为帧协议字节流
	frameBytes := dispatch.FrameToBytes(frame)

	//将序列化的字节流数据写入连接的缓存区
	if _, err := wc.BufWR.Write(frameBytes); err != nil {
		return err
	}

	//将数据从缓存里移到io
	if err := wc.BufWR.Flush(); err != nil {
		return err
	}

	return nil
}

// constructDataFrame 构造数据类型的帧（binary、text）
func constructDataFrame(payload []byte, noMask bool, msgType dispatch.MessageType) *dispatch.Frame {
	final := msgType != dispatch.ContinuationFrame
	frame := constructFrame(msgType, final, noMask)
	frame.SetPayload(payload)

	return frame
}

// constructControlFrame 构造控制帧（ping、pong、close）
func constructControlFrame(msgType dispatch.MessageType, isServer bool, payload []byte) *dispatch.Frame {
	//只有客户端想服务端发送帧时，才会对帧进行掩码处理
	frame := constructFrame(msgType, true, isServer)

	if len(payload) > 0 {
		frame.SetPayload(payload)
	}

	return frame
}

func constructFrame(msgType dispatch.MessageType, final bool, noMask bool) *dispatch.Frame {

	fin := uint16(1)
	mask := uint16(1)
	//不是最后一个帧，需要设置fin字段为0
	if !final {
		fin = 0
	}
	//没有mask，标志mask为0
	if noMask {
		mask = 0
	}

	frame := dispatch.Frame{
		Fin:                fin,
		RSV1:               0,
		RSV2:               0,
		RSV3:               0,
		OpCode:             uint16(msgType),
		Mask:               mask,
		PayloadLen:         0,
		PayloadExtendLen16: 0,
		PayloadExtendLen64: 0,
		MaskingKey:         0,
	}

	//需要时设置maskingKey字段
	if frame.Mask == 1 {
		(&frame).CreateMaskingKey()
	}

	return &frame
}

// CloseRight 正常关闭连接
func (wc *WsConn) CloseRight() error {
	return close(wc, dispatch.CloseRight)
}

// CloseAsideLeaving 某端点离开连接
func (wc *WsConn) CloseAsideLeaving() error {
	return close(wc, dispatch.CloseAsideLeaving)
}

// CloseWrongProtocol 表示端点由于协议错误正在终止连接
func (wc *WsConn) CloseWrongProtocol() error {
	return close(wc, dispatch.CloseWrongProtocol)
}

// CloseNotAccept 关闭连接，因某端点接收到一种它不能接受的数据
func (wc *WsConn) CloseNotAccept() error {
	return close(wc, dispatch.CloseNotAccept)
}

// CloseAbnormal 异常关闭
func (wc *WsConn) CloseAbnormal() error {
	return close(wc, dispatch.CloseAbnormal)
}

// CloseDifferentMsgType 表示端点正在终止连接 ，因为它在消息中接收到与消息类型不一致的数据
func (wc *WsConn) CloseDifferentMsgType() error {
	return close(wc, dispatch.CloseDifferentMsgType)
}

// CloseInternalError 端点内部错误，关闭连接
func (wc *WsConn) CloseInternalError() error {
	return close(wc, dispatch.CloseInternalError)
}

// CloseTooBigData 表示端点正在终止连接 ，因为它收到了一条太大而无法处理的消息。
func (wc *WsConn) CloseTooBigData() error {
	return close(wc, dispatch.CloseTooBigData)
}

func close(wc *WsConn, closeCode int) (err error) {
	p := make([]byte, 2, 16)
	//前两个字节放入code
	binary.BigEndian.PutUint16(p[:2], uint16(closeCode))
	//后续放入原因
	p = append(p, []byte(dispatch.Error(closeCode))...)
	log.Printf("c.close sending close frame, payload=%s", p)

	wc.State = Closing
	//发送关闭帧
	if err = sendControlFrame(wc, dispatch.ConnectionCloseFrame, p); err != nil {
		log.Printf("c.handleClose failed to c.sendControlFrame, err=%v", err)
		return
	}

	//关闭底层tcp连接
	if wc.Conn != nil {
		defer func(wc *WsConn) {
			err := closeTcp(wc)
			if err != nil {
				log.Println("close tcp err: ", err)
			}
		}(wc)
	}

	//连接状态
	wc.State = Closed

	return nil
}

// closeTcp 关闭底层tcp
func closeTcp(wc *WsConn) error {
	return wc.Conn.Close()
}
