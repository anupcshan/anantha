// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        v3.21.12
// source: carrier_info.proto

package carrier

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type ConfigType int32

const (
	ConfigType_CT_UNKNOWN ConfigType = 0
	ConfigType_CT_TEMP    ConfigType = 1 // Temp in C
	ConfigType_CT_INT16   ConfigType = 2 // Also used for small values. May be negative.
	ConfigType_CT_INT     ConfigType = 3 // Mostly temp in F (system/oat)
	ConfigType_CT_UINT16  ConfigType = 5 // Generally used for values under 100, like percentages and temp in F - not negative
	ConfigType_CT_INT32   ConfigType = 6
	ConfigType_CT_INT64   ConfigType = 7 // What's the difference betweeen 6 and 7? 7 seems to be used for yearly kWh values, while 6 is used for monthly/weekly
	ConfigType_CT_SEQ     ConfigType = 8 // This seems to be used only for "bdSeq", with id 11 set to int(5) or int(2). Probably a sequence number
	ConfigType_CT_FLOAT   ConfigType = 9
	ConfigType_CT_BOOL    ConfigType = 11 // ??
	ConfigType_CT_STRING  ConfigType = 12 // ??
	ConfigType_CT_BYTES   ConfigType = 17 // ??
	ConfigType_CT_STRUCT  ConfigType = 19 // ??
)

// Enum value maps for ConfigType.
var (
	ConfigType_name = map[int32]string{
		0:  "CT_UNKNOWN",
		1:  "CT_TEMP",
		2:  "CT_INT16",
		3:  "CT_INT",
		5:  "CT_UINT16",
		6:  "CT_INT32",
		7:  "CT_INT64",
		8:  "CT_SEQ",
		9:  "CT_FLOAT",
		11: "CT_BOOL",
		12: "CT_STRING",
		17: "CT_BYTES",
		19: "CT_STRUCT",
	}
	ConfigType_value = map[string]int32{
		"CT_UNKNOWN": 0,
		"CT_TEMP":    1,
		"CT_INT16":   2,
		"CT_INT":     3,
		"CT_UINT16":  5,
		"CT_INT32":   6,
		"CT_INT64":   7,
		"CT_SEQ":     8,
		"CT_FLOAT":   9,
		"CT_BOOL":    11,
		"CT_STRING":  12,
		"CT_BYTES":   17,
		"CT_STRUCT":  19,
	}
)

func (x ConfigType) Enum() *ConfigType {
	p := new(ConfigType)
	*p = x
	return p
}

func (x ConfigType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ConfigType) Descriptor() protoreflect.EnumDescriptor {
	return file_carrier_info_proto_enumTypes[0].Descriptor()
}

func (ConfigType) Type() protoreflect.EnumType {
	return &file_carrier_info_proto_enumTypes[0]
}

func (x ConfigType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ConfigType.Descriptor instead.
func (ConfigType) EnumDescriptor() ([]byte, []int) {
	return file_carrier_info_proto_rawDescGZIP(), []int{0}
}

type Detail struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Entries       []*ConfigSetting `protobuf:"bytes,2,rep,name=entries,proto3" json:"entries,omitempty"`
	DetailTypeStr string           `protobuf:"bytes,4,opt,name=detail_type_str,json=detailTypeStr,proto3" json:"detail_type_str,omitempty"`
	Zero          int32            `protobuf:"varint,5,opt,name=zero,proto3" json:"zero,omitempty"`
}

func (x *Detail) Reset() {
	*x = Detail{}
	if protoimpl.UnsafeEnabled {
		mi := &file_carrier_info_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Detail) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Detail) ProtoMessage() {}

func (x *Detail) ProtoReflect() protoreflect.Message {
	mi := &file_carrier_info_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Detail.ProtoReflect.Descriptor instead.
func (*Detail) Descriptor() ([]byte, []int) {
	return file_carrier_info_proto_rawDescGZIP(), []int{0}
}

func (x *Detail) GetEntries() []*ConfigSetting {
	if x != nil {
		return x.Entries
	}
	return nil
}

func (x *Detail) GetDetailTypeStr() string {
	if x != nil {
		return x.DetailTypeStr
	}
	return ""
}

func (x *Detail) GetZero() int32 {
	if x != nil {
		return x.Zero
	}
	return 0
}

type ConfigSetting struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name            string     `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	TimestampMillis int64      `protobuf:"varint,3,opt,name=timestamp_millis,json=timestampMillis,proto3" json:"timestamp_millis,omitempty"`
	ConfigType      ConfigType `protobuf:"varint,4,opt,name=config_type,json=configType,proto3,enum=carrier.ConfigType" json:"config_type,omitempty"`
	// Types that are assignable to Value:
	//
	//	*ConfigSetting_ConfigTypeCopy
	//	*ConfigSetting_IntValue
	//	*ConfigSetting_AnotherIntValue
	//	*ConfigSetting_FloatValue
	//	*ConfigSetting_BoolValue
	//	*ConfigSetting_MaybeStrValue
	//	*ConfigSetting_BytesValue
	Value   isConfigSetting_Value `protobuf_oneof:"value"`
	Details []*Detail             `protobuf:"bytes,18,rep,name=details,proto3" json:"details,omitempty"`
}

func (x *ConfigSetting) Reset() {
	*x = ConfigSetting{}
	if protoimpl.UnsafeEnabled {
		mi := &file_carrier_info_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ConfigSetting) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ConfigSetting) ProtoMessage() {}

func (x *ConfigSetting) ProtoReflect() protoreflect.Message {
	mi := &file_carrier_info_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ConfigSetting.ProtoReflect.Descriptor instead.
func (*ConfigSetting) Descriptor() ([]byte, []int) {
	return file_carrier_info_proto_rawDescGZIP(), []int{1}
}

func (x *ConfigSetting) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ConfigSetting) GetTimestampMillis() int64 {
	if x != nil {
		return x.TimestampMillis
	}
	return 0
}

func (x *ConfigSetting) GetConfigType() ConfigType {
	if x != nil {
		return x.ConfigType
	}
	return ConfigType_CT_UNKNOWN
}

func (m *ConfigSetting) GetValue() isConfigSetting_Value {
	if m != nil {
		return m.Value
	}
	return nil
}

func (x *ConfigSetting) GetConfigTypeCopy() ConfigType {
	if x, ok := x.GetValue().(*ConfigSetting_ConfigTypeCopy); ok {
		return x.ConfigTypeCopy
	}
	return ConfigType_CT_UNKNOWN
}

func (x *ConfigSetting) GetIntValue() int32 {
	if x, ok := x.GetValue().(*ConfigSetting_IntValue); ok {
		return x.IntValue
	}
	return 0
}

func (x *ConfigSetting) GetAnotherIntValue() int32 {
	if x, ok := x.GetValue().(*ConfigSetting_AnotherIntValue); ok {
		return x.AnotherIntValue
	}
	return 0
}

func (x *ConfigSetting) GetFloatValue() float32 {
	if x, ok := x.GetValue().(*ConfigSetting_FloatValue); ok {
		return x.FloatValue
	}
	return 0
}

func (x *ConfigSetting) GetBoolValue() bool {
	if x, ok := x.GetValue().(*ConfigSetting_BoolValue); ok {
		return x.BoolValue
	}
	return false
}

func (x *ConfigSetting) GetMaybeStrValue() []byte {
	if x, ok := x.GetValue().(*ConfigSetting_MaybeStrValue); ok {
		return x.MaybeStrValue
	}
	return nil
}

func (x *ConfigSetting) GetBytesValue() []byte {
	if x, ok := x.GetValue().(*ConfigSetting_BytesValue); ok {
		return x.BytesValue
	}
	return nil
}

func (x *ConfigSetting) GetDetails() []*Detail {
	if x != nil {
		return x.Details
	}
	return nil
}

type isConfigSetting_Value interface {
	isConfigSetting_Value()
}

type ConfigSetting_ConfigTypeCopy struct {
	ConfigTypeCopy ConfigType `protobuf:"varint,7,opt,name=config_type_copy,json=configTypeCopy,proto3,enum=carrier.ConfigType,oneof"` // Appears to have same value as 4 in a lot of cases.
}

type ConfigSetting_IntValue struct {
	IntValue int32 `protobuf:"varint,10,opt,name=int_value,json=intValue,proto3,oneof"`
}

type ConfigSetting_AnotherIntValue struct {
	AnotherIntValue int32 `protobuf:"varint,11,opt,name=another_int_value,json=anotherIntValue,proto3,oneof"` // Used for intervals, requestID, counters, hours, .... All values are < 9000?
}

type ConfigSetting_FloatValue struct {
	FloatValue float32 `protobuf:"fixed32,12,opt,name=float_value,json=floatValue,proto3,oneof"`
}

type ConfigSetting_BoolValue struct {
	BoolValue bool `protobuf:"varint,14,opt,name=bool_value,json=boolValue,proto3,oneof"`
}

type ConfigSetting_MaybeStrValue struct {
	MaybeStrValue []byte `protobuf:"bytes,15,opt,name=maybe_str_value,json=maybeStrValue,proto3,oneof"`
}

type ConfigSetting_BytesValue struct {
	BytesValue []byte `protobuf:"bytes,16,opt,name=bytes_value,json=bytesValue,proto3,oneof"`
}

func (*ConfigSetting_ConfigTypeCopy) isConfigSetting_Value() {}

func (*ConfigSetting_IntValue) isConfigSetting_Value() {}

func (*ConfigSetting_AnotherIntValue) isConfigSetting_Value() {}

func (*ConfigSetting_FloatValue) isConfigSetting_Value() {}

func (*ConfigSetting_BoolValue) isConfigSetting_Value() {}

func (*ConfigSetting_MaybeStrValue) isConfigSetting_Value() {}

func (*ConfigSetting_BytesValue) isConfigSetting_Value() {}

type CarrierInfo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	TimestampMillis int64            `protobuf:"varint,1,opt,name=timestamp_millis,json=timestampMillis,proto3" json:"timestamp_millis,omitempty"`
	ConfigSettings  []*ConfigSetting `protobuf:"bytes,2,rep,name=config_settings,json=configSettings,proto3" json:"config_settings,omitempty"`
	Counter         int32            `protobuf:"varint,3,opt,name=counter,proto3" json:"counter,omitempty"` // What is this?
	Uuid            string           `protobuf:"bytes,4,opt,name=uuid,proto3" json:"uuid,omitempty"`
}

func (x *CarrierInfo) Reset() {
	*x = CarrierInfo{}
	if protoimpl.UnsafeEnabled {
		mi := &file_carrier_info_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *CarrierInfo) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CarrierInfo) ProtoMessage() {}

func (x *CarrierInfo) ProtoReflect() protoreflect.Message {
	mi := &file_carrier_info_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CarrierInfo.ProtoReflect.Descriptor instead.
func (*CarrierInfo) Descriptor() ([]byte, []int) {
	return file_carrier_info_proto_rawDescGZIP(), []int{2}
}

func (x *CarrierInfo) GetTimestampMillis() int64 {
	if x != nil {
		return x.TimestampMillis
	}
	return 0
}

func (x *CarrierInfo) GetConfigSettings() []*ConfigSetting {
	if x != nil {
		return x.ConfigSettings
	}
	return nil
}

func (x *CarrierInfo) GetCounter() int32 {
	if x != nil {
		return x.Counter
	}
	return 0
}

func (x *CarrierInfo) GetUuid() string {
	if x != nil {
		return x.Uuid
	}
	return ""
}

var File_carrier_info_proto protoreflect.FileDescriptor

var file_carrier_info_proto_rawDesc = []byte{
	0x0a, 0x12, 0x63, 0x61, 0x72, 0x72, 0x69, 0x65, 0x72, 0x5f, 0x69, 0x6e, 0x66, 0x6f, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x12, 0x07, 0x63, 0x61, 0x72, 0x72, 0x69, 0x65, 0x72, 0x22, 0x76, 0x0a,
	0x06, 0x44, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x12, 0x30, 0x0a, 0x07, 0x65, 0x6e, 0x74, 0x72, 0x69,
	0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x16, 0x2e, 0x63, 0x61, 0x72, 0x72, 0x69,
	0x65, 0x72, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x53, 0x65, 0x74, 0x74, 0x69, 0x6e, 0x67,
	0x52, 0x07, 0x65, 0x6e, 0x74, 0x72, 0x69, 0x65, 0x73, 0x12, 0x26, 0x0a, 0x0f, 0x64, 0x65, 0x74,
	0x61, 0x69, 0x6c, 0x5f, 0x74, 0x79, 0x70, 0x65, 0x5f, 0x73, 0x74, 0x72, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0d, 0x64, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x54, 0x79, 0x70, 0x65, 0x53, 0x74,
	0x72, 0x12, 0x12, 0x0a, 0x04, 0x7a, 0x65, 0x72, 0x6f, 0x18, 0x05, 0x20, 0x01, 0x28, 0x05, 0x52,
	0x04, 0x7a, 0x65, 0x72, 0x6f, 0x22, 0xd7, 0x03, 0x0a, 0x0d, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67,
	0x53, 0x65, 0x74, 0x74, 0x69, 0x6e, 0x67, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x29, 0x0a, 0x10, 0x74,
	0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x5f, 0x6d, 0x69, 0x6c, 0x6c, 0x69, 0x73, 0x18,
	0x03, 0x20, 0x01, 0x28, 0x03, 0x52, 0x0f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70,
	0x4d, 0x69, 0x6c, 0x6c, 0x69, 0x73, 0x12, 0x34, 0x0a, 0x0b, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67,
	0x5f, 0x74, 0x79, 0x70, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x13, 0x2e, 0x63, 0x61,
	0x72, 0x72, 0x69, 0x65, 0x72, 0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x54, 0x79, 0x70, 0x65,
	0x52, 0x0a, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x54, 0x79, 0x70, 0x65, 0x12, 0x3f, 0x0a, 0x10,
	0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x5f, 0x74, 0x79, 0x70, 0x65, 0x5f, 0x63, 0x6f, 0x70, 0x79,
	0x18, 0x07, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x13, 0x2e, 0x63, 0x61, 0x72, 0x72, 0x69, 0x65, 0x72,
	0x2e, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x54, 0x79, 0x70, 0x65, 0x48, 0x00, 0x52, 0x0e, 0x63,
	0x6f, 0x6e, 0x66, 0x69, 0x67, 0x54, 0x79, 0x70, 0x65, 0x43, 0x6f, 0x70, 0x79, 0x12, 0x1d, 0x0a,
	0x09, 0x69, 0x6e, 0x74, 0x5f, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x0a, 0x20, 0x01, 0x28, 0x05,
	0x48, 0x00, 0x52, 0x08, 0x69, 0x6e, 0x74, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x2c, 0x0a, 0x11,
	0x61, 0x6e, 0x6f, 0x74, 0x68, 0x65, 0x72, 0x5f, 0x69, 0x6e, 0x74, 0x5f, 0x76, 0x61, 0x6c, 0x75,
	0x65, 0x18, 0x0b, 0x20, 0x01, 0x28, 0x05, 0x48, 0x00, 0x52, 0x0f, 0x61, 0x6e, 0x6f, 0x74, 0x68,
	0x65, 0x72, 0x49, 0x6e, 0x74, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x21, 0x0a, 0x0b, 0x66, 0x6c,
	0x6f, 0x61, 0x74, 0x5f, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x0c, 0x20, 0x01, 0x28, 0x02, 0x48,
	0x00, 0x52, 0x0a, 0x66, 0x6c, 0x6f, 0x61, 0x74, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x1f, 0x0a,
	0x0a, 0x62, 0x6f, 0x6f, 0x6c, 0x5f, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x0e, 0x20, 0x01, 0x28,
	0x08, 0x48, 0x00, 0x52, 0x09, 0x62, 0x6f, 0x6f, 0x6c, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x28,
	0x0a, 0x0f, 0x6d, 0x61, 0x79, 0x62, 0x65, 0x5f, 0x73, 0x74, 0x72, 0x5f, 0x76, 0x61, 0x6c, 0x75,
	0x65, 0x18, 0x0f, 0x20, 0x01, 0x28, 0x0c, 0x48, 0x00, 0x52, 0x0d, 0x6d, 0x61, 0x79, 0x62, 0x65,
	0x53, 0x74, 0x72, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x21, 0x0a, 0x0b, 0x62, 0x79, 0x74, 0x65,
	0x73, 0x5f, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x10, 0x20, 0x01, 0x28, 0x0c, 0x48, 0x00, 0x52,
	0x0a, 0x62, 0x79, 0x74, 0x65, 0x73, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x29, 0x0a, 0x07, 0x64,
	0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x18, 0x12, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0f, 0x2e, 0x63,
	0x61, 0x72, 0x72, 0x69, 0x65, 0x72, 0x2e, 0x44, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x52, 0x07, 0x64,
	0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x42, 0x07, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x22,
	0xa7, 0x01, 0x0a, 0x0b, 0x43, 0x61, 0x72, 0x72, 0x69, 0x65, 0x72, 0x49, 0x6e, 0x66, 0x6f, 0x12,
	0x29, 0x0a, 0x10, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x5f, 0x6d, 0x69, 0x6c,
	0x6c, 0x69, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x0f, 0x74, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x4d, 0x69, 0x6c, 0x6c, 0x69, 0x73, 0x12, 0x3f, 0x0a, 0x0f, 0x63, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x5f, 0x73, 0x65, 0x74, 0x74, 0x69, 0x6e, 0x67, 0x73, 0x18, 0x02, 0x20,
	0x03, 0x28, 0x0b, 0x32, 0x16, 0x2e, 0x63, 0x61, 0x72, 0x72, 0x69, 0x65, 0x72, 0x2e, 0x43, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x53, 0x65, 0x74, 0x74, 0x69, 0x6e, 0x67, 0x52, 0x0e, 0x63, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x53, 0x65, 0x74, 0x74, 0x69, 0x6e, 0x67, 0x73, 0x12, 0x18, 0x0a, 0x07, 0x63,
	0x6f, 0x75, 0x6e, 0x74, 0x65, 0x72, 0x18, 0x03, 0x20, 0x01, 0x28, 0x05, 0x52, 0x07, 0x63, 0x6f,
	0x75, 0x6e, 0x74, 0x65, 0x72, 0x12, 0x12, 0x0a, 0x04, 0x75, 0x75, 0x69, 0x64, 0x18, 0x04, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x04, 0x75, 0x75, 0x69, 0x64, 0x2a, 0xc1, 0x01, 0x0a, 0x0a, 0x43, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x54, 0x79, 0x70, 0x65, 0x12, 0x0e, 0x0a, 0x0a, 0x43, 0x54, 0x5f, 0x55,
	0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12, 0x0b, 0x0a, 0x07, 0x43, 0x54, 0x5f, 0x54,
	0x45, 0x4d, 0x50, 0x10, 0x01, 0x12, 0x0c, 0x0a, 0x08, 0x43, 0x54, 0x5f, 0x49, 0x4e, 0x54, 0x31,
	0x36, 0x10, 0x02, 0x12, 0x0a, 0x0a, 0x06, 0x43, 0x54, 0x5f, 0x49, 0x4e, 0x54, 0x10, 0x03, 0x12,
	0x0d, 0x0a, 0x09, 0x43, 0x54, 0x5f, 0x55, 0x49, 0x4e, 0x54, 0x31, 0x36, 0x10, 0x05, 0x12, 0x0c,
	0x0a, 0x08, 0x43, 0x54, 0x5f, 0x49, 0x4e, 0x54, 0x33, 0x32, 0x10, 0x06, 0x12, 0x0c, 0x0a, 0x08,
	0x43, 0x54, 0x5f, 0x49, 0x4e, 0x54, 0x36, 0x34, 0x10, 0x07, 0x12, 0x0a, 0x0a, 0x06, 0x43, 0x54,
	0x5f, 0x53, 0x45, 0x51, 0x10, 0x08, 0x12, 0x0c, 0x0a, 0x08, 0x43, 0x54, 0x5f, 0x46, 0x4c, 0x4f,
	0x41, 0x54, 0x10, 0x09, 0x12, 0x0b, 0x0a, 0x07, 0x43, 0x54, 0x5f, 0x42, 0x4f, 0x4f, 0x4c, 0x10,
	0x0b, 0x12, 0x0d, 0x0a, 0x09, 0x43, 0x54, 0x5f, 0x53, 0x54, 0x52, 0x49, 0x4e, 0x47, 0x10, 0x0c,
	0x12, 0x0c, 0x0a, 0x08, 0x43, 0x54, 0x5f, 0x42, 0x59, 0x54, 0x45, 0x53, 0x10, 0x11, 0x12, 0x0d,
	0x0a, 0x09, 0x43, 0x54, 0x5f, 0x53, 0x54, 0x52, 0x55, 0x43, 0x54, 0x10, 0x13, 0x42, 0x29, 0x5a,
	0x27, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x61, 0x6e, 0x75, 0x70,
	0x63, 0x73, 0x68, 0x61, 0x6e, 0x2f, 0x61, 0x6e, 0x61, 0x6e, 0x74, 0x68, 0x61, 0x2f, 0x70, 0x62,
	0x2f, 0x63, 0x61, 0x72, 0x72, 0x69, 0x65, 0x72, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_carrier_info_proto_rawDescOnce sync.Once
	file_carrier_info_proto_rawDescData = file_carrier_info_proto_rawDesc
)

func file_carrier_info_proto_rawDescGZIP() []byte {
	file_carrier_info_proto_rawDescOnce.Do(func() {
		file_carrier_info_proto_rawDescData = protoimpl.X.CompressGZIP(file_carrier_info_proto_rawDescData)
	})
	return file_carrier_info_proto_rawDescData
}

var file_carrier_info_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_carrier_info_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_carrier_info_proto_goTypes = []any{
	(ConfigType)(0),       // 0: carrier.ConfigType
	(*Detail)(nil),        // 1: carrier.Detail
	(*ConfigSetting)(nil), // 2: carrier.ConfigSetting
	(*CarrierInfo)(nil),   // 3: carrier.CarrierInfo
}
var file_carrier_info_proto_depIdxs = []int32{
	2, // 0: carrier.Detail.entries:type_name -> carrier.ConfigSetting
	0, // 1: carrier.ConfigSetting.config_type:type_name -> carrier.ConfigType
	0, // 2: carrier.ConfigSetting.config_type_copy:type_name -> carrier.ConfigType
	1, // 3: carrier.ConfigSetting.details:type_name -> carrier.Detail
	2, // 4: carrier.CarrierInfo.config_settings:type_name -> carrier.ConfigSetting
	5, // [5:5] is the sub-list for method output_type
	5, // [5:5] is the sub-list for method input_type
	5, // [5:5] is the sub-list for extension type_name
	5, // [5:5] is the sub-list for extension extendee
	0, // [0:5] is the sub-list for field type_name
}

func init() { file_carrier_info_proto_init() }
func file_carrier_info_proto_init() {
	if File_carrier_info_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_carrier_info_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*Detail); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_carrier_info_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*ConfigSetting); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_carrier_info_proto_msgTypes[2].Exporter = func(v any, i int) any {
			switch v := v.(*CarrierInfo); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	file_carrier_info_proto_msgTypes[1].OneofWrappers = []any{
		(*ConfigSetting_ConfigTypeCopy)(nil),
		(*ConfigSetting_IntValue)(nil),
		(*ConfigSetting_AnotherIntValue)(nil),
		(*ConfigSetting_FloatValue)(nil),
		(*ConfigSetting_BoolValue)(nil),
		(*ConfigSetting_MaybeStrValue)(nil),
		(*ConfigSetting_BytesValue)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_carrier_info_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_carrier_info_proto_goTypes,
		DependencyIndexes: file_carrier_info_proto_depIdxs,
		EnumInfos:         file_carrier_info_proto_enumTypes,
		MessageInfos:      file_carrier_info_proto_msgTypes,
	}.Build()
	File_carrier_info_proto = out.File
	file_carrier_info_proto_rawDesc = nil
	file_carrier_info_proto_goTypes = nil
	file_carrier_info_proto_depIdxs = nil
}
