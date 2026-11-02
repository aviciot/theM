# 3D Models Directory

Place your `.glb` or `.gltf` files here.

## Setup

1. **Add GLB files** to this directory:
   ```
   /opt/docker/them/product-site/public/models/
   ```

2. **Access in viewer:**
   - Files are served from `/models/filename.glb`
   - Example: `example.glb` → `/models/example.glb`

3. **Test in 3D Lab:**
   - Navigate to: http://localhost:3001/3d-lab
   - Or via Traefik: http://localhost:8088/product/3d-lab

## Recommended Tools

**Free GLB models:**
- Sketchfab (https://sketchfab.com)
- Poly Pizza (https://poly.pizza)
- Google Poly Archive

**Create/Edit GLB:**
- Blender (free, export as GLB)
- Spline (web-based 3D design)
- Ready Player Me (avatars)

## File Format

- **Preferred:** `.glb` (binary, smaller)
- **Supported:** `.gltf` (JSON + separate assets)
- **Max size:** ~10MB (for web performance)

## Examples

```
models/
├── logo.glb          # the-M logo in 3D
├── example.glb       # Sample model
└── hero-object.glb   # For hero section
```
