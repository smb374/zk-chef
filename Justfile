fmt:
    gofmt -l -w .

typecheck:
    go vet ./...

test-one name:
    go test ./... -run '{{name}}' -count=1

test:
    go test ./... -count=1

add pkg:
    go get {{pkg}}

tidy:
    go mod tidy
