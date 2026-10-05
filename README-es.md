# kube-dra-exporter

Copyright 2026 Gradiant. Distribuido bajo la [licencia Apache 2.0](LICENSE).

Responsables del mantenimiento:

- Carlos Giraldo — [@cgiraldo](https://github.com/cgiraldo), [cgiraldo@gradiant.org](mailto:cgiraldo@gradiant.org)
- Antía Rodal — [@arodalfer](https://github.com/arodalfer), [arodal@gradiant.org](mailto:arodal@gradiant.org)

Este es un exportador personalizado de Prometheus escrito en Go que recopila métricas en tiempo real sobre el estado de los `ResourceClaims` (`resource.k8s.io/v1`) en un clúster de Kubernetes. 

Está diseñado específicamente para entornos que utilizan la API nativa de asignación dinámica de recursos (DRA), como entornos de Inteligencia Artificial o HPC que gestionan asignaciones dinámicas de GPUs (NVIDIA/MIG) u otros aceleradores físicos.

## 🚀 Características

- **Diseño Eficiente:** Utiliza un colector dinámico personalizado (`prometheus.Collector`) que consulta la API de Kubernetes bajo demanda en cada raspado (*scrape*), evitando fugas de memoria o métricas obsoletas de recursos eliminados.
- **Acceso Tipado Seguro:** Implementa el cliente tipado estándar de Kubernetes (`clientset`), garantizando rendimiento óptimo y compatibilidad con el esquema oficial de la API de Kubernetes.
- **Doble Compatibilidad de Configuración:** Detecta de forma automática si se ejecuta localmente (usando el archivo `~/.kube/config`) o dentro de un contenedor en Kubernetes (usando un `ServiceAccount`).

## 📊 Métricas Expuestas

El exportador expone las siguientes métricas a través del endpoint `/metrics` en el puerto `8080`:


| Nombre de la Métrica | Tipo | Descripción | Etiquetas (Labels) |
| :--- | :--- | :--- | :--- |
| `kube_resourceclaim_status_allocated_device` | Gauge | Muestra `1.0` si un dispositivo físico específico ha sido asignado de forma exitosa. | `resourceclaim_name`, `namespace`, `pool`, `device_name`, `driver` |
| `kube_resourceclaim_status_reserved_for_pod` | Gauge | Muestra `1.0` por cada Pod activo que retiene actualmente el uso exclusivo de este reclamo. | `resourceclaim_name`, `namespace`, `pod_name`, `uid` |
| `kube_resourceclaim_status_reserved_total` | Gauge | Número de Pods que reservan el ResourceClaim. | `resourceclaim_name`, `namespace` |
| `kube_resourceslice_device_capacity_memory_bytes` | Gauge | Memoria del dispositivo en bytes. | `resourceslice_name`, `device_name`, `driver`, `pool`, `node_name`, `type`, `product_name`, `parent_uuid` |
| `kube_resourceslice_device_capacity_multiprocessors` | Gauge | Número de multiprocesadores. | `resourceslice_name`, `device_name`, `driver`, `pool`, `node_name`, `type`, `product_name`, `parent_uuid` |
| `kube_resourceslice_device_capacity_decoders` | Gauge | Número de decodificadores. | `resourceslice_name`, `device_name`, `driver`, `pool`, `node_name`, `type`, `product_name`, `parent_uuid` |
| `kube_deviceclass_hourly_cost` | Gauge | Coste por hora de la anotación `hourly-cost`. | `deviceclass_name` |

Los errores al consultar cualquiera de las tres APIs hacen que `/metrics` devuelva HTTP 500 para evitar un scrape parcial exitoso. `/healthz` comprueba el proceso sin consultar la API. La anotación `hourly-cost` acepta números decimales no negativos con punto (por ejemplo, `2.50`); rechaza comas, exponentes, NaN e infinitos. Las anotaciones inválidas se omiten y se registran. Fuera del clúster, `KUBECONFIG` admite varios archivos separados por el separador de rutas del sistema.

Las métricas de capacidad incluyen `driver`, `pool` y `node_name`. Relaciona las
asignaciones mediante `(driver, pool, device_name)` dentro del mismo clúster y
objetivo de scrape. `node_name` procede del ResourceSlice o del dispositivo cuando
está habilitada la selección de nodo por dispositivo; queda vacío para recursos
compartidos o definidos mediante selectores. El exportador sigue mostrando todas
las generaciones de pools y las réplicas producen series independientes. Filtra
las generaciones solapadas y deduplica las réplicas antes de relacionar métricas
o calcular totales; estas etiquetas no garantizan por sí solas una coincidencia
única entre slices u objetivos.

## 📥 Quick Install

Instalación de kube-dra-exporter con Helm:

```bash
helm upgrade --install kube-dra-exporter \
  oci://ghcr.io/gradiant/charts/kube-dra-exporter \
  --version 0.1.0 --namespace monitoring --create-namespace
```

## 🛠️ Requisitos Previos

- **Go:** Versión 1.26 o superior (igual que `go.mod` y la imagen de compilación).
- **Kubernetes:** v1.34 o superior, con `resourceclaims`, `resourceslices` y `deviceclasses` disponibles en `resource.k8s.io/v1`. Consulta las [notas de DRA en v1.34](https://v1-34.docs.kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/).
- **Módulos de Go principales:**
  - `github.com/prometheus/client_golang/prometheus`
  - `k8s.io/client-go`
  - `k8s.io/api`

## 💻 Desarrollo Local

1. Descarga las dependencias del módulo existente:
   ```bash
   go mod download
   ```

2. Ejecuta el exportador directamente en tu terminal. Utilizará automáticamente tus credenciales locales configuradas en `kubectl`:
   ```bash
   go run main.go
   ```

3. Verifica el funcionamiento abriendo tu navegador o usando `curl`:
   ```bash
   curl http://localhost:8080/metrics
   ```

## ☸️ Despliegue en Kubernetes

Para desplegar este exportador de forma segura dentro del clúster, requiere permisos `list` en todo el clúster para `resourceclaims`, `resourceslices` y `deviceclasses`. Los ResourceClaims se recopilan en todos los namespaces; los otros dos recursos tienen ámbito de clúster.

Crea un archivo llamado `deploy.yaml` con el siguiente contenido para configurar el **RBAC y el Despliegue**:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-dra-exporter-sa
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-dra-exporter-role
rules:
- apiGroups: ["resource.k8s.io"]
  resources: ["resourceclaims", "resourceslices", "deviceclasses"]
  verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-dra-exporter-binding
subjects:
- kind: ServiceAccount
  name: kube-dra-exporter-sa
  namespace: monitoring
roleRef:
  kind: ClusterRole
  name: kube-dra-exporter-role
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-dra-exporter
  namespace: monitoring
  labels:
    app: kube-dra-exporter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-dra-exporter
  template:
    metadata:
      labels:
        app: kube-dra-exporter
    spec:
      serviceAccountName: kube-dra-exporter-sa
      containers:
      - name: exporter
        image: ghcr.io/gradiant/kube-dra-exporter:0.1.0
        ports:
        - containerPort: 8080
          name: http
        resources:
          limits:
            cpu: 100m
            memory: 128Mi
          requests:
            cpu: 50m
            memory: 64Mi
---
apiVersion: v1
kind: Service
metadata:
  name: kube-dra-exporter
  namespace: monitoring
  labels:
    app: kube-dra-exporter
spec:
  ports:
  - port: 8080
    targetPort: 8080
    name: http
  selector:
    app: kube-dra-exporter
```

Aplica el manifiesto en tu clúster:
```bash
kubectl apply -f deploy.yaml
```

## Configuración Helm

Después de publicar la versión `0.1.0`, instala el chart desde GHCR:

```bash
helm upgrade --install kube-dra-exporter \
  oci://ghcr.io/gradiant/charts/kube-dra-exporter \
  --version 0.1.0 --namespace monitoring --create-namespace
```

Las imágenes publicadas incluyen variantes `linux/amd64`, `linux/arm64`,
`linux/ppc64le`, `linux/s390x` y `linux/riscv64` bajo el mismo tag. El Dockerfile compila para la plataforma solicitada y usa la imagen
distroless Debian 13 correspondiente. Para compilar todas las variantes sin publicar:

```bash
docker buildx build --platform linux/amd64,linux/arm64,linux/ppc64le,linux/s390x,linux/riscv64 \
  --output type=oci,dest=/tmp/kube-dra-exporter.tar .
```

Los contextos de seguridad del Pod y del contenedor son configurables; los administradores
deben aplicar las reglas de admisión mediante
[Pod Security Admission](https://kubernetes.io/docs/concepts/security/pod-security-admission/)
u otro mecanismo de políticas de admisión compatible.

El chart exige permisos en todo el clúster y rechaza `rbac.useClusterRole=false`.
`rbac.useExistingRole` permite usar un ClusterRole existente; con `rbac.create=false`
los permisos deben gestionarse externamente. `namespace` cambia el destino del
despliegue, pero no limita el ámbito de recopilación.

Las réplicas recopilan de forma independiente, sin elección de líder. Evita sumar
métricas duplicadas de distintas réplicas. La estrategia predeterminada es
RollingUpdate. `podDisruptionBudget.enabled=true` crea un PDB con `minAvailable: 1`
por defecto; configura `minAvailable` o `maxUnavailable`, nunca ambos. Con una
sola réplica, este límite puede bloquear el drenaje de un nodo.

El exportador no implementa flags. El chart rechaza `extraArgs` y `featureGates`
no vacíos, y la opción obsoleta `maxConcurrentChallenges`. Configura las credenciales
mediante el ServiceAccount o `KUBECONFIG` con `extraEnv` y archivos montados.

`networkPolicy.enabled=true` crea una política que requiere un CNI que aplique
[NetworkPolicies](https://kubernetes.io/docs/concepts/services-networking/network-policies/).
Por defecto solo permite scrapers con la etiqueta `app.kubernetes.io/name=prometheus`
en el mismo namespace, usando `http` (8080). `prod-values.yaml` permite esos Pods
en `monitoring`; adapta los selectores a los Pods reales del scraper. La salida
permite TCP 443/6443 para la API y TCP/UDP 53 para DNS hacia cualquier destino.
Adapta `networkPolicy.egress` a las direcciones y puertos del clúster si necesitas
restricciones de destino o DNS/API con otros puertos. Verifica la conectividad y
el aislamiento en el clúster tras activar la política.

## 🔍 Integración con Prometheus / VictoriaMetrics

Si utilizas el **Prometheus Operator**, puedes añadir un recurso `ServiceMonitor` para que el sistema comience a recolectar automáticamente las métricas generadas:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kube-dra-exporter-sm
  namespace: monitoring
  labels:
    release: prometheus # Adjust according to your Helm/Prometheus label
spec:
  selector:
    matchLabels:
      app: kube-dra-exporter
  endpoints:
  - port: http
    interval: 15s
```

## Pruebas de regresión

Ejecuta `go test -race ./...`, `go vet ./...` y `go build ./...`.
Para el chart, instala Helm y `PyYAML==6.0.2` y ejecuta
`python -m unittest discover -s tests -v` y
`helm lint deploy/helm/kube-dra-exporter --strict`.
GitHub Actions ejecuta las pruebas en pull requests y pushes de ramas; todos los
workflows de publicación de imágenes y charts exigen que pasen primero.

## Publicación de versiones

El repositorio público es `https://github.com/gradiant/kube-dra-exporter`.
La primera versión es `0.1.0`, con `version` y `appVersion` de `Chart.yaml`
establecidos en `0.1.0`.

Un único tag SemVer, como `0.1.0` (o `v0.1.0`), ejecuta la validación, publica
la imagen para todas las arquitecturas y después publica el chart. El tag debe
coincidir con `version` y `appVersion`; actualiza ambos en cada versión.
Deja `image.tag` vacío en los valores distribuidos para usar
`appVersion`: un tag distinto o un digest fijado bloquea la validación. Los usuarios
pueden personalizar tags y digests al instalar. La versión de la aplicación debe
ser un tag Docker válido, sin metadatos SemVer de compilación (`+...`).

Las versiones se publican en GitHub Packages (GHCR):

- Imagen: `ghcr.io/gradiant/kube-dra-exporter:0.1.0`
- Chart: `oci://ghcr.io/gradiant/charts/kube-dra-exporter`, versión `0.1.0`
