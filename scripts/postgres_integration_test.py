import json
import os
import secrets
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import quote


ROOT = Path(__file__).resolve().parent.parent
SERVICE = "wb-postgresql"


def run(args, stage, *, env=None, capture=False, secret=None):
    result = subprocess.run(
        args,
        cwd=ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.STDOUT if capture else None,
        check=False,
    )
    if capture and result.stdout and stage != "чтение конфигурации Docker Compose":
        output = result.stdout
        if secret:
            output = output.replace(secret, "[скрыто]").replace(quote(secret, safe=""), "[скрыто]")
        print(output, end="" if output.endswith("\n") else "\n")
    if result.returncode:
        raise RuntimeError(f"{stage}: код выхода {result.returncode}")
    return result.stdout if capture else None


def compose_environment():
    raw = run(
        ["docker", "compose", "config", "--format", "json"],
        "чтение конфигурации Docker Compose",
        capture=True,
    )
    config = json.loads(raw)
    environment = config["services"][SERVICE]["environment"]
    if isinstance(environment, list):
        environment = dict(entry.split("=", 1) for entry in environment)
    user = environment.get("POSTGRES_USER")
    password = environment.get("POSTGRES_PASSWORD")
    database = environment.get("POSTGRES_DB")
    if not user or not password or not database:
        raise ValueError("в .env нужны POSTGRES_USER, POSTGRES_PASSWORD и POSTGRES_DB")
    return user, password, database


def database_url(user, password, host, database):
    return (
        f"postgres://{quote(user, safe='')}:{quote(password, safe='')}"
        f"@{host}:5432/{quote(database, safe='')}?sslmode=disable"
    )


def wait_for_postgres(user, database):
    for _ in range(60):
        result = subprocess.run(
            ["docker", "compose", "exec", "-T", SERVICE, "pg_isready", "-U", user, "-d", database],
            cwd=ROOT,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if result.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError("PostgreSQL не стал доступен за 60 секунд")


def main():
    database = None
    created = False
    try:
        user, password, primary_database = compose_environment()
        database = f"wb_it_{int(time.time())}_{os.getpid()}_{secrets.token_hex(2)}"
        run(["docker", "compose", "up", "-d", SERVICE], "запуск PostgreSQL")
        wait_for_postgres(user, primary_database)
        run(
            ["docker", "compose", "exec", "-T", SERVICE, "createdb", "-U", user, database],
            "создание тестовой БД",
        )
        created = True
        print(f"Создана тестовая БД: {database}", flush=True)
        run(
            [
                "docker", "compose", "run", "--rm", "wb-postgresql-migrate",
                "-path", "/migration", "-database",
                database_url(user, password, SERVICE, database), "up",
            ],
            "применение миграций",
            capture=True,
            secret=password,
        )
        run(["docker", "compose", "up", "-d", "port-forwarder"], "запуск проброса порта")
        test_env = os.environ.copy()
        test_env["POSTGRES_TEST_DSN"] = database_url(user, password, "127.0.0.1", database)
        run(["make", "postgres-integration-test"], "интеграционные тесты", env=test_env)
        run(
            ["docker", "compose", "exec", "-T", SERVICE, "dropdb", "-U", user, database],
            "удаление тестовой БД",
        )
        print("Интеграционные тесты прошли; тестовая БД удалена")
        return 0
    except (OSError, RuntimeError, ValueError, KeyError, json.JSONDecodeError) as error:
        print(f"Ошибка: {error}", file=sys.stderr)
        if created:
            print(f"Тестовая БД сохранена для диагностики: {database}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
