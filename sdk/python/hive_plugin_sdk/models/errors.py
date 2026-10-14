class HiveProtocolError(RuntimeError):
    def __init__(self, code: str, message: str, method: str | None = None) -> None:
        self.code = code
        self.method = method
        super().__init__(message)

