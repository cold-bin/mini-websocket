// @author cold bin
// @date 2022/7/24

package dispatch

import (
	"encoding/binary"
	"errors"
	"log"
	"math/rand"
	"time"
)

const (
	CloseRight            = 1000 //连接正常关闭
	CloseAsideLeaving     = 1001 // 表示端点正在“离开”，例如服务器关闭或浏览器已离开页面
	CloseWrongProtocol    = 1002 // 表示端点由于协议错误正在终止连接。
	CloseNotAccept        = 1003 //关闭连接，因某端点接收到一种它不能接受的数据
	CloseAbnormal         = 1006 //异常关闭
	CloseDifferentMsgType = 1007 //表示端点正在终止连接 ，因为它在消息中接收到与消息类型不一致的数据
	CloseTooBigData       = 1009 //表示端点正在终止连接 ，因为它收到了一条太大而无法处理的消息。
)

var closeErrorMap = map[int]string{
	CloseRight: "正常，关闭连接",

	CloseAsideLeaving:     "服务器关闭或浏览器已离开页面，关闭连接",
	CloseWrongProtocol:    "协议错误，关闭连接",
	CloseNotAccept:        "浏览器或服务器接收到不能接受的数据，关闭连接",
	CloseAbnormal:         "未发送关闭帧，关闭连接",
	CloseDifferentMsgType: "消息类型不一致，关闭连接",
	CloseTooBigData:       "消息太大，关闭连接",
}

// Error 返回指定关闭连接的原因
func Error(code int) string {
	return closeErrorMap[code]
}

const (
	minFrameHeaderByteSize = 2 + 4     // header(2 uint8)+MaskingKey(4 uint8 \ 0 uint8)
	maxFrameHeaderByteSize = 2 + 8 + 4 // header(2 uint8)+payloadExtLen(8 uint8)+MaskingKey(4 uint8)

	maxControlFramePayloadByteSize = 125
)

// MessageType 指定帧的 Frame.OpCode 类型
type MessageType uint16

//消息帧的类型
const (
	NoFrame           MessageType = 0xFFFF //该值表示不是消息帧
	ContinuationFrame MessageType = 0x0
	TextFrame         MessageType = 0x1
	BinaryFrame       MessageType = 0x2

	//0x3 ~ 7, 目前保留, 以后将用作更多的非控制类 frame

	ConnectionCloseFrame MessageType = 0x8
	PingFrame            MessageType = 0x9
	PongFrame            MessageType = 0xA

	//0xB ~ F, 目前保留, 以后将用作更多的控制类 frame
)

type Frame struct {
	Fin    uint16 // 1 bit，消息分段时，值为0；没有分段，值为1.分片消息的结尾数据包这个应该设置为1
	RSV1   uint16 // 1 bit, 0
	RSV2   uint16 // 1 bit, 0
	RSV3   uint16 // 1 bit, 0
	OpCode uint16 // 4 bits，最大值为0-15
	Mask   uint16 // 1 bit

	PayloadLen uint16 // 7 bits，当 PayloadLen 在(0,125]时，表示该值存在且为真实字节长度

	PayloadExtendLen16 uint16 // 16 bits，仅当 PayloadLen 为126时，表示该值存在且是真实字节长度
	PayloadExtendLen64 uint64 // 64 bits，仅当 PayloadLen 为127时，表示该值存在且是真实字节长度

	MaskingKey uint32 // 32 bits，如果 Frame.Mask 设置为 1，该字段则占4bits；否则，占0个字节
	Payload    []byte //负载数据，
}

var (
	errFrameFin          = errors.New("frame fin值应该设置为1或0")
	errFrameRSV1         = errors.New("frame rsv1值应该设置为1或0")
	errFrameRSV2         = errors.New("frame rsv2值应该设置为1或0")
	errFrameRSV3         = errors.New("frame rsv3值应该设置为1或0")
	errFrameOpCode       = errors.New("frame opcode值应该设置为0~15")
	errFrameMask         = errors.New("frame mask值应该设置为0或1")
	errFrameMaskingKey   = errors.New("frame maskingKey值应该设置")
	errFramePayloadLen   = errors.New("frame payload len 值应该为0~125")
	errFramePayloadLen16 = errors.New("frame payload len 值应该为126")
	errFramePayloadLen64 = errors.New("frame payload len 值应该为127")
	//errFrameIsTooBig     = errors.New("frame payload 太大，应该小于或等于 " + strconv.Itoa(maxControlFramePayloadByteSize) + "字节")
)

// CheckFrameWithoutPayload 检验 Frame 格式，以便完成序列化，这里不校验掩码处理的Payload
func CheckFrameWithoutPayload(frame *Frame) error {
	log.Println("frame: ", frame)
	if !(frame.Fin == 1 || frame.Fin == 0) {
		return errFrameFin
	}
	if !(frame.RSV1 == 1 || frame.RSV1 == 0) {
		return errFrameRSV1
	}
	if !(frame.RSV2 == 1 || frame.RSV2 == 0) {
		return errFrameRSV2
	}
	if !(frame.RSV3 == 1 || frame.RSV3 == 0) {
		return errFrameRSV3
	}
	if frame.OpCode < 0 || frame.OpCode > 15 {
		return errFrameOpCode
	}
	if !(frame.Mask == 1 || frame.Mask == 0) {
		return errFrameMask
	}

	//if !(frame.Mask == 1 && frame.MaskingKey != 0) {
	//	return errFrameMaskingKey
	//}

	if !(frame.PayloadLen > 0 && frame.PayloadLen <= 125) {
		switch frame.PayloadLen {
		case 126:
			if frame.PayloadExtendLen16 == 0 {
				return errFramePayloadLen16
			}
			return nil
		case 127:
			if frame.PayloadExtendLen64 == 0 {
				return errFramePayloadLen64
			}
			return nil
		}
		return errFramePayloadLen
	}

	return nil
}

// FrameToBytes 将 Frame 中的各个字段，按照websocket协议标准，剔除无关位，并序列化
// 为字节流数据，此前应该调用 CheckFrameWithoutPayload 检验
func FrameToBytes(frame *Frame) []byte {
	buf := make([]byte, 2, minFrameHeaderByteSize+8)

	// 该部分为协议帧里的前16位，即从 Frame.Fin 至 Frame.PayloadLen
	var part1 uint16

	//或运算不改变其他非0位
	part1 |= (frame.Fin) << 15
	part1 |= (frame.RSV1) << 14
	part1 |= (frame.RSV2) << 13
	part1 |= (frame.RSV3) << 12
	part1 |= (frame.OpCode) << 8 //此字段占4位
	part1 |= (frame.Mask) << 7

	//先处理一部分，如果后续PayloadLen大于125，再往切片后面追加数据即可
	part1 |= frame.PayloadLen

	//将 part1 填入字节流的前两个字节，也就是前16位
	binary.BigEndian.PutUint16(buf[:2], part1)

	switch frame.PayloadLen {
	case 126:
		//Payload Len Ext1 启用，长度16bit，扩展16个bit
		payloadExtendBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(payloadExtendBuf[:2], frame.PayloadExtendLen16)
		buf = append(buf, payloadExtendBuf...)
	case 127:
		//Payload Len Ext2 启用，长度64bit，扩展64个bit（显然包括Payload Len Ext1）
		payloadExtendBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(payloadExtendBuf[:8], frame.PayloadExtendLen64)
		buf = append(buf, payloadExtendBuf...)
	}

	//当 Frame.Mask==1时，需要设置32位的MaskingKey
	if frame.Mask == 1 {
		maskingKeyBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(maskingKeyBuf[:4], frame.MaskingKey)
		buf = append(buf, maskingKeyBuf...)
	}

	//追加 payload
	buf = append(buf, frame.Payload...)

	return buf
}

// ParseToFrameHeader 先处理头部帧，只有头部帧是确定长度的，
// 头部字节流之后的payloadLenExt(1/2)、makingKey是无法确定的
// 需要根据头部帧进行解析，所以先解析出头部帧数据
func ParseToFrameHeader(frameBytes []byte) *Frame {
	frame := new(Frame)
	//header数据
	part1 := binary.BigEndian.Uint16(frameBytes[:2]) //129 139 | 1000 0001 1000 1011
	log.Println("header: ", part1)
	//先与运算，从16位的bit里取出各个字段

	frame.Fin = (part1 & 0x8000) >> 15  // part1 & 1000 0000 0000 0000
	frame.RSV1 = (part1 & 0x4000) >> 14 // part1 & 0100 0000 0000 0000
	frame.RSV2 = part1 & 0x2000 >> 13   // part1 & 0010 0000 0000 0000
	frame.RSV3 = part1 & 0x1000 >> 12   // part1 & 0001 0000 0000 0000
	frame.OpCode = part1 & 0x0f00 >> 8  // part1 & 0000 1111 0000 0000
	frame.Mask = part1 & 0x0080 >> 7    // part1 & 0000 0000 1000 0000
	frame.PayloadLen = part1 & 0x007f   // part1 & 0000 0000 0111 1111

	return frame
}

// CreateMaskingKey 随机数生成器随机生成 32 比特的 Masking-Key
func (f *Frame) CreateMaskingKey() {
	rand.Seed(time.Now().UnixNano())
	f.MaskingKey = rand.Uint32()
}

func (f *Frame) MaskPayload() {
	//masks := make([]byte, 0, 4)
	////扩容机制，影响位数
	////取maskingKey
	//masks = append(masks, uint8((f.MaskingKey>>24)&0x00FF))
	//masks = append(masks, uint8((f.MaskingKey>>16)&0x00FF))
	//masks = append(masks, uint8((f.MaskingKey>>8)&0x00FF))
	//masks = append(masks, uint8((f.MaskingKey)&0x00FF))

	masks := [4]byte{
		uint8((f.MaskingKey >> 24) & 0x00FF),
		uint8((f.MaskingKey >> 16) & 0x00FF),
		uint8((f.MaskingKey >> 8) & 0x00FF),
		uint8((f.MaskingKey) & 0x00FF),
	}
	//掩码算法
	for i, v := range f.Payload {
		j := i % 4
		f.Payload[i] = v ^ masks[j]
	}
}

// CalcPayloadLen 处理frame中的 PayloadLen \ PayloadExtendLen16 \ PayloadExtendLen64
func (f *Frame) CalcPayloadLen() {
	payloadLen := uint64(len(f.Payload))

	if payloadLen <= 125 {
		f.PayloadLen = uint16(payloadLen)
		f.PayloadExtendLen16 = 0
		f.PayloadExtendLen64 = 0
	} else if payloadLen <= 65535 {
		f.PayloadLen = 126
		f.PayloadExtendLen16 = uint16(payloadLen)
		f.PayloadExtendLen64 = 0
	} else {
		f.PayloadLen = 127
		f.PayloadExtendLen16 = 0
		f.PayloadExtendLen64 = payloadLen
	}
}

// SetPayload 设置负载数据，根据情况是否进行掩码处理，矫正 Payload 的长度
func (f *Frame) SetPayload(payload []byte) *Frame {
	f.Payload = payload
	if f.Mask == 1 {
		//掩码处理
		f.MaskPayload()
	}

	f.CalcPayloadLen()
	return f
}

// IsFinal 当协议帧的Fin为1时，表示该帧是结束帧，后续的数据不是连续的
func (f *Frame) IsFinal() bool {
	return f.Fin == 1
}
