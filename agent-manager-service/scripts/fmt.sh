gofumpt -l -w .
# golines -m 100 -w .
gofmt -s -w . # already covered by gofumpt, but keeping it for now
goimports -w -local github.com/wso2/agent-manager/agent-manager-service .
bash scripts/newline.sh
