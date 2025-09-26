# Download Multithread de Arquivo Grande

Aplicação para baixar um arquivo grande utilizando múltiplas threads de forma eficiente.

## Funcionalidades

- **Download paralelo:** cada thread baixa uma faixa específica de bytes.
- **Controle de largura de banda:** divisão entre threads para não sobrecarregar a conexão.
- **Sincronização:** evita conflitos ao escrever no arquivo compartilhado.

## Como rodar

No terminal, rode a seguinte linha de comando:

   ``go run main.go``

E preencha no terminal os campos solicitados, sendo eles:
1. URL de do arquivo a ser baixado

2. Quantidade de Threads que serão utilizadas, atentando-se que um número muito grande pode trazer problemas de limite de requisições no endpoint.

3. Limite de banda em MB/s.


Obs: É necessário ter o [Go](https://go.dev/) instalado.

