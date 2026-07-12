FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY advisor.py .
COPY main.py .
EXPOSE 9600
CMD ["python", "main.py"]
