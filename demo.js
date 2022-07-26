let ws = new WebSocket("ws://127.0.0.1:8080/ws");

ws.onopen = (evt) =>{  //绑定连接事件
    console.log("Connection open ...");
    ws.send("发送的数据");
    console.log(evt.data)
};

ws.onmessage = (evt) => {//绑定收到消息事件
    console.log( "Received Message: " + evt.data);
};

ws.onclose = (evt) => { //绑定关闭或断开连接事件
    console.log("Connection closed.");
};
