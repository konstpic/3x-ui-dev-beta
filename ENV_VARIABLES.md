# Переменные окружения 3x-ui

## Основное приложение (Panel)

### Web Panel настройки (только через env, не доступны в UI)

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `XUI_WEB_PORT` | Порт веб-панели | `2053` | `2053` |
| `XUI_WEB_LISTEN` | IP адрес для прослушивания веб-панели | - | `0.0.0.0` |
| `XUI_WEB_DOMAIN` | Домен веб-панели | - | `panel.example.com` |
| `XUI_WEB_BASE_PATH` | Базовый путь URL для веб-панели | `/` | `/` |
| `XUI_WEB_CERT_FILE` | Путь к SSL сертификату для веб-панели | - | `/app/cert/fullchain.pem` |
| `XUI_WEB_KEY_FILE` | Путь к SSL приватному ключу для веб-панели | - | `/app/cert/privkey.pem` |

### Subscription настройки (только через env, не доступны в UI)

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `XUI_SUB_PORT` | Порт сервиса подписки | `2096` | `2096` |
| `XUI_SUB_PATH` | URI путь для подписки | `/sub/` | `/sub/` |
| `XUI_SUB_DOMAIN` | Домен для сервиса подписки | - | `sub.example.com` |
| `XUI_SUB_CERT_FILE` | Путь к SSL сертификату для подписки | - | `/app/cert/sub-fullchain.pem` |
| `XUI_SUB_KEY_FILE` | Путь к SSL приватному ключу для подписки | - | `/app/cert/sub-privkey.pem` |

### База данных PostgreSQL

| Переменная | Описание | Значение по умолчанию | Обязательная | Пример |
|------------|----------|----------------------|--------------|--------|
| `XUI_DB_HOST` | Хост PostgreSQL | `localhost` | Нет | `postgres` |
| `XUI_DB_PORT` | Порт PostgreSQL | `5432` | Нет | `5432` |
| `XUI_DB_USER` | Пользователь PostgreSQL | - | **Да** | `xui_user` |
| `XUI_DB_PASSWORD` | Пароль PostgreSQL | - | **Да** | `change_this_password` |
| `XUI_DB_NAME` | Имя базы данных | Имя приложения | Нет | `xui_db` |
| `XUI_DB_SSLMODE` | Режим SSL для PostgreSQL | `disable` | Нет | `disable`, `require`, `verify-ca`, `verify-full` |

### Логирование и отладка

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `XUI_LOG_LEVEL` | Уровень логирования | `info` | `debug`, `info`, `notice`, `warning`, `error` |
| `XUI_DEBUG` | Режим отладки | `false` | `true`, `false` |
| `XUI_LOG_FOLDER` | Папка для логов | Зависит от платформы | `/var/log/x-ui` |

### Пути и папки

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `XUI_BIN_FOLDER` | Папка для бинарных файлов | `bin` | `/app/bin` |

### Xray настройки

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `XRAY_VMESS_AEAD_FORCED` | Принудительное использование VMESS AEAD | `false` | `true`, `false` |

### Безопасность

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `XUI_ENABLE_FAIL2BAN` | Включить fail2ban | `true` | `true`, `false` |

---

## Node Service (Worker)


### TLS настройки

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `NODE_TLS_CERT_FILE` | Путь к SSL сертификату для API ноды | - | `/app/cert/node-cert.pem` |
| `NODE_TLS_KEY_FILE` | Путь к SSL приватному ключу для API ноды | - | `/app/cert/node-key.pem` |

*Примечание: `NODE_API_KEY` не обязателен при первом запуске, но нода должна быть зарегистрирована через `/api/v1/register` endpoint.

---

## PostgreSQL (для docker-compose)

| Переменная | Описание | Значение по умолчанию | Пример |
|------------|----------|----------------------|--------|
| `POSTGRES_USER` | Пользователь PostgreSQL | - | `xui_user` |
| `POSTGRES_PASSWORD` | Пароль PostgreSQL | - | `change_this_password` |
| `POSTGRES_DB` | Имя базы данных | - | `xui_db` |

---

## Пример использования в docker-compose.yml

```yaml
services:
  3xui:
    environment:
      # Xray настройки
      XRAY_VMESS_AEAD_FORCED: "false"
      XUI_ENABLE_FAIL2BAN: "true"
      #XUI_LOG_LEVEL: "debug"
      
      # Web Panel настройки (только через env, не доступны в UI)
      # XUI_WEB_PORT: 2053
      # XUI_WEB_LISTEN: 0.0.0.0
      # XUI_WEB_DOMAIN: panel.example.com
      # XUI_WEB_BASE_PATH: /
      # XUI_WEB_CERT_FILE: /app/cert/fullchain.pem
      # XUI_WEB_KEY_FILE: /app/cert/privkey.pem
      
      # Subscription настройки (только через env, не доступны в UI)
      # XUI_SUB_PORT: 2096
      # XUI_SUB_PATH: /sub/
      # XUI_SUB_DOMAIN: sub.example.com
      # XUI_SUB_CERT_FILE: /app/cert/sub-fullchain.pem
      # XUI_SUB_KEY_FILE: /app/cert/sub-privkey.pem
      
      # PostgreSQL настройки
      XUI_DB_HOST: postgres
      XUI_DB_PORT: 5432
      XUI_DB_USER: xui_user
      XUI_DB_PASSWORD: change_this_password
      XUI_DB_NAME: xui_db
      XUI_DB_SSLMODE: disable
```

---

## Примечания

1. **Web Panel и Subscription настройки** (`XUI_WEB_*` и `XUI_SUB_*`) доступны **только через переменные окружения** и не могут быть изменены через веб-интерфейс.

2. **База данных**: `XUI_DB_USER`, `XUI_DB_PASSWORD` являются обязательными для работы приложения.


3. Все пути к сертификатам должны быть абсолютными путями внутри контейнера (обычно /app/cert/*.pem).
