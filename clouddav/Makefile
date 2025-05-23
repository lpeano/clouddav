# Makefile per la build dell'applicazione Go

# Nome dell'applicazione
APP_NAME = clouddav

# Percorso del modulo Go (sostituisci con il tuo modulo)
# Esempio: se il tuo modulo è github.com/utente/repo, usa quel percorso
# Se il tuo file main.go è nella root del modulo, puoi usare "." o il nome del modulo.
# Per semplicità e robustezza, usiamo il nome del modulo per riferirci al pacchetto principale.
GO_MODULE_PATH = $(GO_MODULE) # Riferimento al pacchetto principale del modulo

# File di configurazione di esempio
CONFIG_EXAMPLE = config.yaml.example

# Directory dei file statici
STATIC_DIR = static

# Porta predefinita per Delve (puoi cambiarla se necessario)
DELVE_PORT = 2345

# Target di default: build per l'OS corrente
.PHONY: all
all: build-current



## Build Standard (Senza Debugging)

.PHONY: build-current
build-current:
	@echo "Building for current OS..."
	go build -o $(APP_NAME) $(GO_MODULE_PATH)

.PHONY: build-linux
build-linux:
	@echo "Building for Linux..."
	go env -w CGO_ENABLED=0 GOOS=linux GOARCH=amd64
	go build -o $(APP_NAME)-linux $(GO_MODULE_PATH)
	go env -u CGO_ENABLED GOOS GOARCH # Ripristina le variabili d'ambiente Go

.PHONY: build-windows
build-windows:
	@echo "Building for Windows..."
	go env -w CGO_ENABLED=0 GOOS=windows GOARCH=amd64
	go build -o $(APP_NAME)-windows.exe $(GO_MODULE_PATH)
	go env -u CGO_ENABLED GOOS GOARCH # Ripristina le variabili d'ambiente Go



## Build per Debug Remoto



.PHONY: build-debug-current
build-debug-current:
	@echo "Building for current OS with debug info..."
	go build -gcflags="all=-N -l" -o $(APP_NAME)-debug $(GO_MODULE_PATH)

.PHONY: build-debug-linux
build-debug-linux:
	@echo "Building for Linux with debug info..."
	go env -w CGO_ENABLED=0 GOOS=linux GOARCH=amd64
	go build -gcflags="all=-N -l" -o $(APP_NAME)-linux-debug $(GO_MODULE_PATH)
	go env -u CGO_ENABLED GOOS GOARCH

.PHONY: build-debug-windows
build-debug-windows:
	@echo "Building for Windows with debug info..."
	go env -w CGO_ENABLED=0 GOOS=windows GOARCH=amd64
	go build -gcflags="all=-N -l" -o $(APP_NAME)-windows-debug.exe $(GO_MODULE_PATH)
	go env -u CGO_ENABLED GOOS GOARCH
	



## Esecuzione con Debug Remoto


.PHONY: run-debug-current
run-debug-current: build-debug-current
	@echo "Starting $(APP_NAME) on current OS for remote debug on port $(DELVE_PORT)..."
	# Assicurati che dlv sia installato e nel tuo PATH locale
	dlv debug $(APP_NAME)-debug --headless --listen=:$(DELVE_PORT) --api-version=2 --log

.PHONY: run-debug-linux
run-debug-linux: build-debug-linux
	@echo "To debug on a remote Linux machine:"
	@echo "1. Copy $(APP_NAME)-linux-debug to the remote server."
	@echo "2. Install dlv on the remote server: go install github.com/go-delve/delve/cmd/dlv@latest"
	@echo "3. Run on remote server: dlv debug ./$(APP_NAME)-linux-debug --headless --listen=:$(DELVE_PORT) --api-version=2 --log"
	@echo "4. Remember to open port $(DELVE_PORT) in the firewall or use an SSH tunnel."

.PHONY: run-debug-windows
run-debug-windows: build-debug-windows
	@echo "To debug on a remote Windows machine:"
	@echo "1. Copy $(APP_NAME)-windows-debug.exe to the remote server."
	@echo "2. Install dlv on the remote server: go install github.com/go-delve/delve/cmd/dlv@latest"
	@echo "3. Run on remote server: dlv debug ./$(APP_NAME)-windows-debug.exe --headless --listen=:$(DELVE_PORT) --api-version=2 --log"
	@echo "4. Remember to open port $(DELVE_PORT) in the firewall."



## Docker con Debug Remoto



.PHONY: docker-build-debug
docker-build-debug:
	@echo "Building Docker image with debug capabilities..."
	docker build -f Dockerfile.debug -t $(APP_NAME):debug .

.PHONY: docker-run-debug
docker-run-debug: docker-build-debug
	@echo "Running Docker container for remote debug on port $(DELVE_PORT)..."
	docker run -p $(DELVE_PORT):$(DELVE_PORT) \
	-v ./config.yaml:/app/config.yaml \
	$(APP_NAME):debug



## Pulizia e Utilità

.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -f $(APP_NAME) $(APP_NAME)-debug \
		$(APP_NAME)-linux $(APP_NAME)-linux-debug \
		$(APP_NAME)-windows.exe $(APP_NAME)-windows-debug.exe

.PHONY: docker-clean
docker-clean:
	@echo "Removing Docker images..."
	docker rmi $(APP_NAME):latest || true
	docker rmi $(APP_NAME):debug || true

.PHONY: docker-build
docker-build: build-linux
	@echo "Building Docker image..."
	docker build -t $(APP_NAME):latest .

.PHONY: run
run: build-current
	@echo "Running application locally..."
	CONFIG_FILE=$(CONFIG_EXAMPLE) ./${APP_NAME}

.PHONY: copy-config-example
copy-config-example:
	@echo "Copying config.yaml.example to config.yaml (if not exists)..."
	cp -n $(CONFIG_EXAMPLE) config.yaml

.PHONY: docker-run
docker-run: docker-build
	@echo "Running Docker container..."
	docker run -p 8080:8080 \
	-v ./config.yaml:/app/config.yaml \
	$(APP_NAME):latest