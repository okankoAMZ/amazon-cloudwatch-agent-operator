FROM python:3.8-slim-bookworm

WORKDIR /root/

COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

CMD [ "python", "./app.py" ]
