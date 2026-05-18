FROM python:3.13-slim

WORKDIR /app
COPY app.py /app/app.py

ENV PORT=8080
ENV SQLITE_PATH=/var/lib/skiff/sqlite/app.db
EXPOSE 8080

CMD ["python", "/app/app.py"]
