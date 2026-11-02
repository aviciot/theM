#!/bin/bash
# Install dependencies inside the running container
docker exec them_gateway-them-product-site-1 npm install --legacy-peer-deps
