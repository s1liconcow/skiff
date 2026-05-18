FROM python:3.13-slim

RUN pip install --no-cache-dir slatedb

WORKDIR /app
COPY app.py /app/app.py

ENV PORT=8080
ENV SLATEDB_URI=memory:///
ENV SLATEDB_TABLE=orders
EXPOSE 8080

CMD ["python", "/app/app.py"]
