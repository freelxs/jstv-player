class _Exceptions:
    class InsecureRequestWarning(Warning):
        pass

    class HTTPError(Exception):
        pass


exceptions = _Exceptions()


class _Retry:
    def __init__(self, *args, **kwargs):
        pass


class _Util:
    class retry:
        Retry = _Retry


util = _Util()


def disable_warnings(category=None):
    return None
