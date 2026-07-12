BIN := webhook-gateway

.PHONY: build tui demo run test clean

build:
	go build -o $(BIN) .

tui: build
	./$(BIN) tui

demo: build
	./$(BIN) tui --demo

run: build
	./$(BIN)

test:
	go test ./...

clean:
	rm -f $(BIN)
