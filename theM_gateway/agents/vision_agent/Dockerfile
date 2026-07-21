FROM python:3.12-slim

WORKDIR /app

# Install dependencies first (layer cache)
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY agent.py .

ENV PORT=9100
ENV AGENT_VERSION=1.0.0
ENV PYTHONUNBUFFERED=1

EXPOSE 9100

# Use exec form so uvicorn is PID 1 and receives SIGTERM directly.
# PORT is baked as an ENV default; override via docker compose environment.
CMD ["uvicorn", "agent:app", "--host", "0.0.0.0", "--port", "9100"]
