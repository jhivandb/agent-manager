set -eu

# gofumpt and gofmt abandon the whole directory walk at the first directory they
# cannot open, so a single unreadable path leaves most of the tree unformatted.
# Name the offenders up front rather than let the walk truncate in silence.
unreadable=$(find . -path ./.git -prune -o -type d ! -exec test -r {} \; -print 2>/dev/null || true)
if [ -n "$unreadable" ]; then
    echo "fmt: unreadable directories would silently truncate the format walk:" >&2
    echo "$unreadable" | sed 's/^/  /' >&2
    echo "fmt: remove them or make them readable, then re-run." >&2
    exit 1
fi

gofumpt -l -w .
# golines -m 100 -w .
gofmt -s -w . # already covered by gofumpt, but keeping it for now
goimports -w -local github.com/wso2/ai-agent-management-platform/agent-manager-service .
bash scripts/newline.sh
