def detect(data):
    if not data:
        return {"encoding": "utf-8", "confidence": 1.0}
    if isinstance(data, str):
        return {"encoding": "utf-8", "confidence": 1.0}
    try:
        data.decode("utf-8")
        return {"encoding": "utf-8", "confidence": 0.99}
    except UnicodeDecodeError:
        pass
    try:
        data.decode("gb18030")
        return {"encoding": "gb18030", "confidence": 0.85}
    except UnicodeDecodeError:
        pass
    return {"encoding": "latin-1", "confidence": 0.5}
