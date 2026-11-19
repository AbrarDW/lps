package builder

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		typ   string
		port  int
		want  []string // substrings expected in the Dockerfile
	}{
		{"next", map[string]string{
			"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.0.0"}}`,
			"package-lock.json": "{}",
		}, "node-next", 3000, []string{"RUN npm ci", "RUN npm run build", `CMD ["npm", "start"]`, "HOSTNAME=0.0.0.0"}},
		{"vite", map[string]string{
			"package.json":   `{"scripts":{"dev":"vite","build":"vite build"},"devDependencies":{"vite":"6.0.0"}}`,
			"pnpm-lock.yaml": "",
		}, "node-spa", 80, []string{"pnpm install --frozen-lockfile", "COPY --from=build /app/dist /usr/share/nginx/html", "try_files"}},
		{"cra", map[string]string{
			"package.json": `{"scripts":{"start":"react-scripts start","build":"react-scripts build"},"dependencies":{"react-scripts":"5"}}`,
		}, "node-spa", 80, []string{"RUN npm install", "/app/build "}},
		{"node-yarn", map[string]string{
			"package.json": `{"main":"server.js","dependencies":{"express":"4"}}`,
			"yarn.lock":    "", "server.js": "",
		}, "node", 3000, []string{"yarn install --frozen-lockfile", `CMD ["node", "server.js"]`}},
		{"django", map[string]string{
			"manage.py": "", "mysite/wsgi.py": "", "requirements.txt": "Django==5.1\n",
		}, "python-django", 8000, []string{"pip install --no-cache-dir -r requirements.txt", "RUN pip install --no-cache-dir gunicorn", `"mysite.wsgi:application"`}},
		{"fastapi", map[string]string{
			"requirements.txt": "fastapi==0.115\nuvicorn[standard]\n", "main.py": "",
		}, "python-fastapi", 8000, []string{`"uvicorn", "main:app"`}},
		{"flask", map[string]string{
			"requirements.txt": "Flask==3.0\n", "app.py": "",
		}, "python-flask", 8000, []string{"pip install --no-cache-dir gunicorn", `"gunicorn", "app:app"`}},
		{"flask-cors-only-is-not-flask", map[string]string{
			"requirements.txt": "flask-cors\n", "main.py": "",
		}, "python", 8000, []string{`CMD ["python", "main.py"]`}},
		{"go-cmd", map[string]string{
			"go.mod": "module x\n", "cmd/server/main.go": "package main\n",
		}, "go", 8080, []string{"go build -o /out/app ./cmd/server"}},
		{"go-root", map[string]string{
			"go.mod": "module x\n", "main.go": "package main\n\nfunc main() {}\n",
		}, "go", 8080, []string{"go build -o /out/app ."}},
		{"laravel", map[string]string{
			"composer.json": "{}", "public/index.php": "",
		}, "php", 80, []string{"APACHE_DOCUMENT_ROOT=/var/www/html/public", "composer install"}},
		{"static", map[string]string{"index.html": "<h1>hi</h1>"}, "static", 80, []string{"FROM nginx:1.27-alpine", "COPY . /usr/share/nginx/html"}},
		{"empty", map[string]string{"README.md": "x"}, "custom", 8080, []string{"FROM ubuntu:24.04"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for f, body := range c.files {
				write(t, dir, f, body)
			}
			d := Detect(dir)
			if d.Type != c.typ || d.Port != c.port {
				t.Fatalf("got %s/%d, want %s/%d\n%s", d.Type, d.Port, c.typ, c.port, d.Dockerfile)
			}
			for _, w := range c.want {
				if !strings.Contains(d.Dockerfile, w) {
					t.Errorf("Dockerfile missing %q:\n%s", w, d.Dockerfile)
				}
			}
			if !strings.Contains(d.Dockerfile, "EXPOSE ") {
				t.Error("no EXPOSE line")
			}
		})
	}
}

func TestTemplatesCoverAll(t *testing.T) {
	ts := Templates(t.TempDir())
	if len(ts) != len(templates) {
		t.Fatalf("got %d templates", len(ts))
	}
	seen := map[string]bool{}
	for _, d := range ts {
		if d.Dockerfile == "" || d.Port == 0 || d.Label == "" || seen[d.Type] {
			t.Fatalf("bad template %+v", d)
		}
		seen[d.Type] = true
	}
}
