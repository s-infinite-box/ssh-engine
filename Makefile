# 定义执行文件
GOCMD=go
# 指定架构和操作系统
export GOARCH=amd64
export GOOS=linux

clean:
	rm -rf bin/*

release: clean
	$(GOCMD) build -o bin/ssh-engine -v cmd/ssh-engine/main.go
	@echo "release success"


