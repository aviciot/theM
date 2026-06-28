FROM python:3.11-slim

# Set environment variables
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/home/auth/.local/bin:$PATH" \
    UV_CACHE_DIR="/home/auth/.cache/uv"

# Create non-root user for security
RUN groupadd -r auth && useradd --no-log-init -r -g auth auth

# Install system dependencies and uv
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Install uv as root
RUN pip install uv

# Ensure uv cache directory is writable for non-root user
RUN mkdir -p /home/auth/.cache/uv && chown -R auth:auth /home/auth/.cache

# Set working directory
WORKDIR /app

# Copy requirements and install Python dependencies using uv
COPY --chown=auth:auth requirements.txt .
RUN uv pip install --system -r requirements.txt

# Copy application code
COPY --chown=auth:auth . .

# Switch to non-root user
USER auth

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8801/health || exit 1

# Expose port
EXPOSE 8801

# Run application
CMD ["python", "main.py"]
