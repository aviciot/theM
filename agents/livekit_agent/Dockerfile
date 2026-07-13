FROM python:3.12-slim
WORKDIR /app
RUN apt-get update && apt-get install -y ffmpeg --no-install-recommends && rm -rf /var/lib/apt/lists/*
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY main.py .
RUN python main.py download-files || true
CMD ["python", "main.py", "start"]
