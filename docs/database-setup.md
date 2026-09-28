# Database setup

Tài liệu này hướng dẫn chạy PostgreSQL local và áp dụng migration cho `gogo-dl`.

## Yêu cầu

- Docker và Docker Compose
- Go 1.23+
- `golang-migrate` CLI nếu chạy migration từ máy host

## Cách chạy local

1. Tạo file cấu hình local:

   ```bash
   cp configs/.env.example .env.local
   ```

   Các giá trị mặc định dùng PostgreSQL tại `localhost:5432`, database `gogo_dl`, user `postgres` và password `postgres`. Hãy chỉnh `.env.local` nếu môi trường của bạn khác. App vẫn hỗ trợ `configs/.env` cũ.

2. Khởi động PostgreSQL:

   ```bash
   make docker-up
   ```

   Lệnh này chỉ khởi động service PostgreSQL và giữ dữ liệu trong volume Docker `postgres_data`.

3. Khởi động API (migration tự chạy trước khi HTTP sẵn sàng):

   ```bash
   make deps
   make run
   ```

API mặc định chạy tại `http://localhost:8080`.

## Chạy toàn bộ bằng Docker Compose

Sau khi tạo `configs/.env` cho Compose, có thể để app và PostgreSQL chạy cùng Compose:

```bash
make docker-up-all
```

Trong container, app dùng hostname `postgres`. Migration SQL đã được nhúng vào binary và chạy khi khởi động, nên không cần chạy `make migrate-up` từ host cho luồng này.

## Xử lý lỗi thường gặp

- `migrate: command not found`: cài CLI bằng:

  ```bash
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```

  Đảm bảo thư mục binary của Go nằm trong `PATH`.

- Không kết nối được tới database: kiểm tra trạng thái bằng `docker compose ps postgres` và log bằng `docker compose logs postgres`.

- Port `5432` trên host đã được sử dụng: chạy `DB_PORT=55432 make docker-up` và đặt `DB_PORT=55432` trong `.env.local` cho app chạy trên host. Với app chạy trong Compose, giữ `DB_PORT=5432` trong `configs/.env` vì app kết nối tới cổng nội bộ của service `postgres`.

Không chạy `make migrate-down` hoặc `make docker-down-v` trên dữ liệu cần giữ lại; các lệnh này có thể rollback schema hoặc xóa volume database.
