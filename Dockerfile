FROM golang:1.18 as builder
WORKDIR /code
COPY go.mod go.sum ./
RUN go mod download
COPY ./ ./
RUN go build -o /anchorhodler ./main.go
RUN WASMVM_SO=$(ldd /anchorhodler | grep libwasmvm.so | awk '{ print $3 }') && ls ${WASMVM_SO} && cp ${WASMVM_SO} /


FROM frolvlad/alpine-glibc
RUN apk update && apk add ca-certificates tzdata && rm -rf /var/cache/apk/*
COPY --from=builder /libwasmvm.so /lib/libwasmvm.so
COPY --from=builder /anchorhodler .
ENV LD_LIBRARY_PATH="${LD_LIBRARY_PATH}:/lib"
CMD /anchorhodler