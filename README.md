# pulse_bot

### DB dump:

```
pg_dump --no-owner -Fc -U postgres pulse_bot -f ./pulse_bot.custom
```

### DB restore:

```
dropdb -U postgres pulse_bot
createdb -U postgres pulse_bot
pg_restore --no-owner -d pulse_bot -U postgres ./pulse_bot.custom
```

### Install `migrate` command-tool:

https://github.com/golang-migrate/migrate/tree/master/cmd/migrate

### Create new migration:

```
migrate create -ext sql -dir migrations mg_name
```

### Apply migration:

```
migrate -path migrations -database "postgres://localhost:5432/db_name?sslmode=disable" up
```

### Install grpc tools:

```
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
```
