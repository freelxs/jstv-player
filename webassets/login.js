document.getElementById('login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const error = document.getElementById('login-error');
  const button = form.querySelector('button');
  error.textContent = '';
  button.disabled = true;
  try {
    const body = new FormData(form);
    const response = await fetch('/api/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-JSTV-Request': '1' },
      body: JSON.stringify({ username: body.get('username'), password: body.get('password') }),
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      error.textContent = data.error || '登录失败';
      return;
    }
    location.href = '/';
  } catch (err) {
    error.textContent = '无法连接服务器';
  } finally {
    button.disabled = false;
  }
});
