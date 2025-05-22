#!/bin/sh
# entry.sh - Generic entrypoint script

# Перший аргумент - це назва команди/бінарника для запуску
COMMAND_TO_RUN=$1

# Якщо команда не передана, виводимо помилку та виходимо
if [ -z "$COMMAND_TO_RUN" ]; then
  echo "Error: No command specified for entrypoint."
  exit 1
fi

# Видаляємо перший аргумент (назву команди) зі списку аргументів,
# щоб решта передалася як аргументи для самої команди.
shift

echo "Entrypoint: Attempting to run command '/opt/app/${COMMAND_TO_RUN}' with args: $@"

# Запускаємо відповідний бінарник з рештою аргументів
# Використовуємо exec, щоб процес замінив собою sh-скрипт
exec "/opt/app/${COMMAND_TO_RUN}" "$@"