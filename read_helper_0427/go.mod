module read_helper

go 1.24.0

require github.com/qdrant/go-client v1.17.1

require (
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect; 兼容 Go 1.23，避免被拉取到需要 1.24 的 v0.34
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/grpc v1.78.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// 解决 qdrant 依赖的 genproto 歧义（多模块提供同一 package）
replace google.golang.org/genproto => google.golang.org/genproto v0.0.0-20240227224415-6ceb2ff114de
