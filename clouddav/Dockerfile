# Fase di build
# Usa un'immagine Go per compilare l'applicazione
FROM golang:latest AS builder

# Imposta la directory di lavoro all'interno del container
WORKDIR /app

# Copia i file del modulo Go (go.mod e go.sum)
# Questo permette a Docker di scaricare le dipendenze in un layer separato
COPY go.mod .
COPY go.sum .

# Scarica le dipendenze
# GO111MODULE=on assicura che Go Modules sia abilitato
RUN GO111MODULE=on go mod download

# Copia il resto del codice sorgente dell'applicazione
COPY . .

# Compila l'applicazione per Linux
# CGO_ENABLED=0 crea un binario statico che non dipende dalle librerie di sistema
# -o specifica il nome del file di output
# /app specifica il percorso del modulo da compilare
RUN CGO_ENABLED=0 GOOS=linux go build -o clouddav /app

# Fase finale (immagine leggera)
# Usa un'immagine base minimale (come alpine o scratch)
# Alpine è comune per la sua piccola dimensione e include alcune utility di base
FROM alpine:latest

# Imposta la directory di lavoro
WORKDIR /app

# Copia il binario compilato dalla fase di build
COPY --from=builder /app/clouddav .


# Copia la directory dei file statici
COPY static ./static

# Copia il file di configurazione di esempio
# È consigliabile montare il file di configurazione reale come un volume in produzione
COPY testing/config.yaml  ./config.yaml

ENV PORT 8180

# Espone la porta su cui l'applicazione ascolta (assicurati che corrisponda alla tua configurazione Go)
EXPOSE 8180

# Definisce il comando predefinito per eseguire l'applicazione
# CONFIG_FILE specifica il nome del file di configurazione da caricare
CMD ["/app/clouddav"]

# Nota:
# - Per l'ambiente di produzione su AKS, dovresti montare il tuo file config.yaml reale
#   come un volume nel pod Kubernetes, invece di copiare config.yaml.example nell'immagine.
#   Questo mantiene la configurazione separata dall'immagine Docker.
# - Assicurati che i percorsi dei filesystem configurati in config.yaml siano accessibili
#   all'interno del container (potrebbe essere necessario montare volumi aggiuntivi).
