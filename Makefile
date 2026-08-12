include .env
include .env.wb
export

PROJECT_ROOT := $(CURDIR)
export PROJECT_ROOT

.PHONY: \
	init \
	env-init \
	env-up \
	env-wait \
	env-down \
	env-port-forward \
	env-port-close \
	env-cleanup \
	migrate-create \
	migrate-up \
	migrate-down \
	migrate-action \
	admin-bootstrap \
	wb-service-run

init: env-init

env-init:
	@$(MAKE) env-up
	@$(MAKE) env-wait
	@$(MAKE) migrate-up
	@$(MAKE) admin-bootstrap

env-up:
	@docker compose up -d wb-postgresql

env-wait:
	@echo "Ожидание готовности PostgreSQL..."
	@until docker compose exec -T wb-postgresql \
		pg_isready \
		-U "$(POSTGRES_USER)" \
		-d "$(POSTGRES_DB)" >/dev/null 2>&1; do \
			sleep 1; \
	done
	@echo "PostgreSQL готов"

env-down:
	@docker compose stop wb-postgresql

env-port-forward:
	@docker compose up -d port-forwarder

env-port-close:
	@docker compose stop port-forwarder
	@docker compose rm -f port-forwarder

env-cleanup:
	@read -p "Очистить файлы PostgreSQL? Возможна потеря данных. [y/N]: " ans; \
	if [ "$$ans" = "y" ]; then \
		docker compose down; \
		rm -rf "$(PROJECT_ROOT)/out/pgdata"; \
		echo "Файлы окружения очищены"; \
	else \
		echo "Очистка окружения отменена"; \
	fi

migrate-create:
	@if [ -z "$(seq)" ]; then \
		echo "Отсутствует параметр seq. Пример: make migrate-create seq=init"; \
		exit 1; \
	fi
	@docker compose run --rm wb-postgresql-migrate \
		create \
		-ext sql \
		-dir /migration \
		-seq "$(seq)"

migrate-up:
	@$(MAKE) migrate-action action=up

migrate-down:
	@$(MAKE) migrate-action action=down

migrate-action:
	@if [ -z "$(action)" ]; then \
		echo "Отсутствует параметр action. Пример: make migrate-action action=up"; \
		exit 1; \
	fi
	@docker compose run --rm wb-postgresql-migrate \
		-path /migration \
		-database "postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@wb-postgresql:5432/$${POSTGRES_DB}?sslmode=disable" \
		"$(action)"

admin-bootstrap:
	@if [ -z "$(ADMIN_TG_ID)" ]; then \
		echo "Ошибка: ADMIN_TG_ID не задан в .env"; \
		exit 1; \
	fi
	@if [ -z "$(ADMIN_FULL_NAME)" ]; then \
		echo "Ошибка: ADMIN_FULL_NAME не задан в .env"; \
		exit 1; \
	fi
	@docker compose exec -T wb-postgresql \
		psql \
		-v ON_ERROR_STOP=1 \
		-v admin_tg_id="$(ADMIN_TG_ID)" \
		-v admin_full_name="$(ADMIN_FULL_NAME)" \
		-U "$(POSTGRES_USER)" \
		-d "$(POSTGRES_DB)" \
		< scripts/bootstrap_admin.sql
	@echo "Admin с TG ID $(ADMIN_TG_ID) создан или обновлён"

wb-service-run:
	@go run cmd/wb-service/main.go
