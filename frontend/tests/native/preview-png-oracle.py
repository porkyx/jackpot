"""Decode an owned native PNG without resizing or editing; print RGBA oracle."""
import hashlib,json,pathlib,sys
from PIL import Image
p=pathlib.Path(sys.argv[1]).resolve()
root=pathlib.Path(__file__).resolve().parents[3]
if not p.is_relative_to(root/".task") or p.suffix.lower()!=".png":
    raise ValueError("owned PNG required")
with Image.open(p) as source:
    if source.format!="PNG" or source.width!=1200 or not 1<=source.height<=8192:
        raise ValueError("bounded native PNG required")
    rgba=source.convert("RGBA")
    pixels=rgba.tobytes()
    print(json.dumps({"width":source.width,"height":source.height,"rgbaBytes":len(pixels),"rgbaSHA256":hashlib.sha256(pixels).hexdigest()}))