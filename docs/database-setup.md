# Database setup

Tài liệu này hướng dẫn chạy PostgreSQL local và áp dụng migration cho `gogo-dl`.

## Yêu cầu

- Docker và Docker Compose
- Go 1.23+
- `golang-migrate` CLI nếu chạy migration từ máy host

## Cách chạy local

1. Tạo file cấu hình local:

   ```bash
   cp configs/.env.example configs/.env
   ```

   Các giá trị mặc định dùng PostgreSQL tại `localhost:5432`, database `gogo_dl`, user `postgres` và password `postgres`. Hãy chỉnh `configs/.env` nếu môi trường của bạn khác.

2. Khởi động PostgreSQL:

   ```bash
   make docker-up
   ```

   Lệnh này chỉ khởi động service PostgreSQL và giữ dữ liệu trong volume Docker `postgres_data`.

3. Áp dụng toàn bộ migration đang có:

   ```bash
   make migrate-up
   ```

4. Khởi động API:

   ```bash
   make deps
   make run
   ```

API mặc định chạy tại `http://localhost:8080`.

## Chạy toàn bộ bằng Docker Compose

Sau khi tạo `configs/.env`, có thể để app và PostgreSQL chạy cùng Compose:

```bash
make docker-up-all
```

Trong container, app dùng hostname `postgres`. App sẽ chạy migration khi khởi động, nên không cần chạy `make migrate-up` từ host cho luồng này.

## Xử lý lỗi thường gặp

- `migrate: command not found`: cài CLI bằng:

  ```bash
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```

  Đảm bảo thư mục binary của Go nằm trong `PATH`.

- Không kết nối được tới database: kiểm tra trạng thái bằng `docker compose ps postgres` và log bằng `docker compose logs postgres`.

- Port `5432` đã được sử dụng: đổi `DB_PORT` trong `configs/.env`, sau đó khởi động lại PostgreSQL và chạy migration với cùng cấu hình.

Không chạy `make migrate-down` hoặc `make docker-down-v` trên dữ liệu cần giữ lại; các lệnh này có thể rollback schema hoặc xóa volume database.
