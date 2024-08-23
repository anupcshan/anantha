pb/carrier_info.pb.go: proto/carrier_info.proto
	protoc --proto_path=proto --go_out=pb --go_opt=paths=source_relative carrier_info.proto
