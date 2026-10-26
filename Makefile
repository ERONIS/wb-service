include .env
export

PROJECT_ROOT := $(CURDIR)

.PHONY: \
	init \
	env-init \
	wb-service-run \
	postgres-integration-test-local \
	migrate-up \
	migrate-down \
	env-up \
	env-wait \
	env-down \
	env-port-forward \
	env-port-close \
	env-cleanup \
	migrate-create \
	migrate-action \
	admin-bootstrap \
	cabinet-bootstrap \
	postgres-integration-test

init: env-init

env-init:
	@$(MAKE) env-up
	@$(MAKE) env-wait
	@$(MAKE) migrate-up
	@$(MAKE) admin-bootstrap
	@$(MAKE) cabinet-bootstrap

wb-service-run:
	@$(MAKE) migrate-up
	@$(MAKE) env-port-forward
	@go run cmd/wb-service/main.go

postgres-integration-test-local:
	@python3 scripts/postgres_integration_test.py

migrate-up:
	@$(MAKE) migrate-action action=up

migrate-down:
	@$(MAKE) migrate-action action="down 1"

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
	if [ "$$ans" = "y" ] || [ "$$ans" = "Y" ]; then \
		set -eu; \
		test -n "$(PROJECT_ROOT)"; \
		docker compose down; \
		docker run --rm --user 0:0 \
			-v "$(PROJECT_ROOT)/.runtime:/workspace-runtime" \
			postgres:18.1-bookworm \
			sh -eu -c 'rm -rf -- /workspace-runtime/pgdata'; \
		test ! -e "$(PROJECT_ROOT)/.runtime/pgdata"; \
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

migrate-action:
	@if [ -z "$(action)" ]; then \
		echo "Отсутствует параметр action. Пример: make migrate-action action=up"; \
		exit 1; \
	fi
	@docker compose run --rm wb-postgresql-migrate \
		-path /migration \
		-database "postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@wb-postgresql:5432/$${POSTGRES_DB}?sslmode=disable" \
		$(action)

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

cabinet-bootstrap:
	@if [ -z "$(ADMIN_TG_ID)" ]; then \
		echo "Ошибка: ADMIN_TG_ID не задан в .env"; \
		exit 1; \
	fi
	@docker compose exec -T wb-postgresql \
		psql \
		-v ON_ERROR_STOP=1 \
		-v admin_tg_id="$(ADMIN_TG_ID)" \
		-U "$(POSTGRES_USER)" \
		-d "$(POSTGRES_DB)" \
		< scripts/bootstrap_cabinets.sql
	@echo "Кабинеты WB для Admin с TG ID $(ADMIN_TG_ID) успешно созданы или обновлены"

postgres-integration-test:
	@if [ -z "$$POSTGRES_TEST_DSN" ]; then \
		echo "Укажите POSTGRES_TEST_DSN для отдельной БД с применёнными миграциями"; \
		exit 1; \
	fi
	@go test -count=1 -v \
		./internal/core/repository/postgres/transaction \
		./internal/core/transport/telegram/storage/postgres \
		./internal/feature/cardimport/repository/postgres \
		./internal/feature/cardpublication/repository/postgres \
		./internal/feature/statistics/repository/postgres \
		./internal/feature/transfer/repository/postgres \
		./internal/feature/users/repository/postgres \
		./internal/feature/wbcabinet/repository/postgres \
		-run '^TestPostgres'
