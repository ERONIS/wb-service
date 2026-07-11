include .env
export

export PROJECT_ROOT=$(shell pwd)

env-up:
	docker compose up -d wb-postgresql

env-down:
	docker compose down wb-postgresql 

env-port-forward:
	@docker compose up -d port-forwarder

env-port-close:
	@docker compose down  port-forwarder

env-cleanup:
	@read -p "Очистить все volume файлы окружения?Опасность утери данных.[y/N]:" ans;\
		if [ "$$ans" = "y" ] ; then \
			docker compose down wb-postgresql && \
			sudo rm -rf out/pgdata && \
		echo "Файлы окружения очищены" ; \
		else \
			echo "Очистка окружения отменена" ; \
		fi
migrate-create:
	@if [ -z "$(seq)" ]; then \
		echo "Отсутствует параметр seq.Пример make migrate-create seq=init"; \
		exit 1; \
	fi;\
	docker compose  run --rm wb-postgresql-migrate \
	create \
	-ext sql \
	-dir /migration \
	-seq "$(seq)"

migrate-up:
	make migrate-action action=up

migrate-down:
	make migrate-action action=down

migrate-action:
	@if [ -z "$(action)" ]; then \
		echo "Отсутствует параметр action. Пример: make migrate-action action=up"; \
		exit 1; \
	fi; \
	docker compose run --rm wb-postgresql-migrate \
		-path /migration \
		-database "postgres://$${POSTGRES_USER}:$${POSTGRES_PASSWORD}@wb-postgresql:5432/$${POSTGRES_DB}?sslmode=disable" \
		"$(action)"
