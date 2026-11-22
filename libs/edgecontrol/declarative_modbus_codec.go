package edgecontrol

import (
	"math"
	"strconv"
	"strings"
	"time"
)

func decodeRegisterSample(spec ModbusRegisterSpec, raw []uint16, at time.Time) Sample {
	switch spec.RawType {
	case ModbusRawBitBool:
		if spec.FunctionCode == ModbusFunctionReadCoils || spec.FunctionCode == ModbusFunctionReadDiscreteInputs {
			return Sample{Value: BooleanValue(raw[0] != 0), Quality: QualityGood, ObservedAt: at}
		}
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		bitSet := (word & (uint16(1) << spec.BitIndex)) != 0
		return Sample{Value: BooleanValue(bitSet), Quality: QualityGood, ObservedAt: at}
	case ModbusRawEnum16:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		key := strconv.FormatUint(uint64(word), 10)
		if label, ok := spec.EnumMap[key]; ok {
			return Sample{Value: StringValue(label), Quality: QualityGood, ObservedAt: at}
		}
		return Sample{Value: StringValue(key), Quality: QualitySuspect, ObservedAt: at}
	case ModbusRawUint16:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		return finiteScaledSample(float64(word)*spec.Scale+spec.Offset, at)
	case ModbusRawInt16:
		word := applyByteOrder16(raw[0], spec.ByteOrder)
		return finiteScaledSample(float64(int16(word))*spec.Scale+spec.Offset, at)
	case ModbusRawUint32:
		u32 := assembleUint32(raw[0], raw[1], spec.ByteOrder, spec.WordOrder)
		return finiteScaledSample(float64(u32)*spec.Scale+spec.Offset, at)
	case ModbusRawInt32:
		u32 := assembleUint32(raw[0], raw[1], spec.ByteOrder, spec.WordOrder)
		return finiteScaledSample(float64(int32(u32))*spec.Scale+spec.Offset, at)
	case ModbusRawFloat32:
		u32 := assembleUint32(raw[0], raw[1], spec.ByteOrder, spec.WordOrder)
		f32 := math.Float32frombits(u32)
		if math.IsNaN(float64(f32)) || math.IsInf(float64(f32), 0) {
			return Sample{Value: DoubleValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
		}
		return finiteScaledSample(float64(f32)*spec.Scale+spec.Offset, at)
	default:
		return Sample{Value: zeroValueForType(spec.channelDataType()), Quality: QualityUnavailable, ObservedAt: at}
	}
}

func finiteScaledSample(val float64, at time.Time) Sample {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return Sample{Value: DoubleValue(0), Quality: QualityBad, QualityReason: QualityReasonNonFiniteFloat, ObservedAt: at}
	}
	return Sample{Value: DoubleValue(val), Quality: QualityGood, ObservedAt: at}
}

func encodeRegisterWrite(spec ModbusRegisterSpec, val Value) ([]uint16, string) {
	switch spec.RawType {
	case ModbusRawEnum16:
		switch val.Type {
		case DataTypeString:
			for rawKey, label := range spec.EnumMap {
				if label == val.String {
					parsed, err := strconv.ParseUint(strings.TrimSpace(rawKey), 10, 16)
					if err != nil {
						return nil, "INVALID_WRITE_VALUE"
					}
					return []uint16{applyByteOrder16(uint16(parsed), spec.ByteOrder)}, ""
				}
			}
			return nil, "INVALID_WRITE_VALUE"
		default:
			num, ok := val.NumericFloat64()
			if !ok || math.IsNaN(num) || math.IsInf(num, 0) || math.Trunc(num) != num || num < 0 || num > 65535 {
				return nil, "INVALID_WRITE_VALUE"
			}
			key := strconv.FormatUint(uint64(num), 10)
			if _, exists := spec.EnumMap[key]; !exists {
				return nil, "INVALID_WRITE_VALUE"
			}
			return []uint16{applyByteOrder16(uint16(num), spec.ByteOrder)}, ""
		}
	default:
		num, ok := val.NumericFloat64()
		if !ok || math.IsNaN(num) || math.IsInf(num, 0) || spec.Scale == 0 {
			return nil, "INVALID_WRITE_VALUE"
		}
		rawFloat := (num - spec.Offset) / spec.Scale
		if math.IsNaN(rawFloat) || math.IsInf(rawFloat, 0) {
			return nil, "INVALID_WRITE_VALUE"
		}
		switch spec.RawType {
		case ModbusRawUint16:
			rounded := math.Round(rawFloat)
			if rounded < 0 || rounded > math.MaxUint16 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return []uint16{applyByteOrder16(uint16(rounded), spec.ByteOrder)}, ""
		case ModbusRawInt16:
			rounded := math.Round(rawFloat)
			if rounded < math.MinInt16 || rounded > math.MaxInt16 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return []uint16{applyByteOrder16(uint16(int16(rounded)), spec.ByteOrder)}, ""
		case ModbusRawUint32:
			rounded := math.Round(rawFloat)
			if rounded < 0 || rounded > math.MaxUint32 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return splitUint32(uint32(rounded), spec.ByteOrder, spec.WordOrder), ""
		case ModbusRawInt32:
			rounded := math.Round(rawFloat)
			if rounded < math.MinInt32 || rounded > math.MaxInt32 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return splitUint32(uint32(int32(rounded)), spec.ByteOrder, spec.WordOrder), ""
		case ModbusRawFloat32:
			if math.Abs(rawFloat) > math.MaxFloat32 {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			f32 := float32(rawFloat)
			if math.IsNaN(float64(f32)) || math.IsInf(float64(f32), 0) {
				return nil, "VALUE_OUT_OF_RANGE"
			}
			return splitUint32(math.Float32bits(f32), spec.ByteOrder, spec.WordOrder), ""
		default:
			return nil, "INVALID_WRITE_VALUE"
		}
	}
}

func applyBitInHoldingRegister(currentWireWord uint16, byteOrder ModbusByteOrder, bitIndex int, bitValue bool) uint16 {
	logical := applyByteOrder16(currentWireWord, byteOrder)
	mask := uint16(1) << bitIndex
	if bitValue {
		logical |= mask
	} else {
		logical &^= mask
	}
	return applyByteOrder16(logical, byteOrder)
}

func applyByteOrder16(word uint16, order ModbusByteOrder) uint16 {
	if order == ModbusByteOrderLittleEndian {
		return (word >> 8) | ((word & 0x00FF) << 8)
	}
	return word
}

func assembleUint32(r0, r1 uint16, byteOrder ModbusByteOrder, wordOrder ModbusWordOrder) uint32 {
	w0 := applyByteOrder16(r0, byteOrder)
	w1 := applyByteOrder16(r1, byteOrder)
	if wordOrder == ModbusWordOrderLittleEndian {
		return (uint32(w1) << 16) | uint32(w0)
	}
	return (uint32(w0) << 16) | uint32(w1)
}

func splitUint32(u32 uint32, byteOrder ModbusByteOrder, wordOrder ModbusWordOrder) []uint16 {
	hi := uint16(u32 >> 16)
	lo := uint16(u32 & 0xFFFF)
	if wordOrder == ModbusWordOrderLittleEndian {
		return []uint16{applyByteOrder16(lo, byteOrder), applyByteOrder16(hi, byteOrder)}
	}
	return []uint16{applyByteOrder16(hi, byteOrder), applyByteOrder16(lo, byteOrder)}
}
