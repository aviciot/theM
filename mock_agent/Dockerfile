FROM python:3.11-slim
WORKDIR /app
RUN pip install --no-cache-dir websockets
COPY agent.py .
CMD ["python", "agent.py"]
